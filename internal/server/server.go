// Package server assembles the listeners.
//
// Up to three, never one: the public listener carries the middleware chain and
// the public routes; the private listener carries /metrics and /healthz; the
// tiles listener carries the self-hosted basemap, and only when one is
// configured. Separate listeners rather than path prefixes, because a prefix is
// one routing mistake away from the wrong outcome — for /metrics, exposing the
// counters that tell a scraper whether it is being throttled; for tiles, either
// rate-limiting a map load into uselessness or carving out an exemption that
// covers more than intended.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"airbg.org/internal/admit"
	"airbg.org/internal/api"
	"airbg.org/internal/config"
	"airbg.org/internal/designkit"
	"airbg.org/internal/httpx"
	"airbg.org/internal/i18n"
	"airbg.org/internal/metrics"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/tiles"
	"airbg.org/internal/web"
)

// Options carries the collaborators plus the whole validated configuration.
// One Config field rather than fifteen scalars: adding a knob then changes no
// signature, and there is no second place for a value to be forgotten.
type Options struct {
	Config    config.Config
	Catalogue *i18n.Catalogue
	Snapshots *snapshot.Holder
	Store     api.DataSource
	Publisher *Publisher
	Logger    *slog.Logger
}

type Server struct {
	public  *http.Server
	private *http.Server
	// tiles is nil when no basemap is configured, which is the shipped
	// configuration. Nil means two listeners, exactly as before.
	tiles         *http.Server
	limiter       *ratelimit.Limiter
	breadth       *ratelimit.Breadth
	seriesLimiter *ratelimit.Limiter
	log           *slog.Logger
	maxConns      int32
	// One eviction interval per limiter, because each limiter has its own key
	// (see startEvicting). A single shared interval silently ignored
	// ratelimit.series.evict_interval for as long as both values happened to be
	// equal in airbg.yaml.
	apiEvictInterval    time.Duration
	seriesEvictInterval time.Duration
	shutdownGrace       time.Duration
}

// maxBodyBytes: this service answers GETs. Anything larger than a generously
// sized header block is not a request we serve. Not configurable: it is not an
// operational knob an operator would ever need to move, unlike the timeouts and
// limits below, which airbg.yaml controls.
const maxBodyBytes = 64 << 10

func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Catalogue == nil || opts.Snapshots == nil {
		return nil, errors.New("server: Catalogue and Snapshots are required")
	}

	resolver, err := httpx.NewIPResolver(opts.Config.Listen.TrustedProxyCIDRs)
	if err != nil {
		return nil, fmt.Errorf("server: trusted proxies: %w", err)
	}

	renderer, err := web.NewRenderer(opts.Catalogue, opts.Snapshots, opts.Config)
	if err != nil {
		return nil, fmt.Errorf("server: renderer: %w", err)
	}

	limiter := ratelimit.New(opts.Config.RateLimit.API, opts.Config.RateLimit.ShardCount)
	seriesLimiter := api.NewSeriesLimiter(opts.Config)
	breadth := ratelimit.NewBreadth(opts.Config.RateLimit.Enumerate)

	// Built here, not left for api.NewRouter's fail-closed default, so its size
	// is the operator's configured value rather than the package-level fallback.
	admission, err := admit.New(int(opts.Config.Database.MaxInflight))
	if err != nil {
		return nil, fmt.Errorf("server: admission: %w", err)
	}

	apiMux := api.NewRouter(api.Deps{
		Config:        opts.Config,
		Snapshots:     opts.Snapshots,
		Breadth:       breadth,
		Store:         opts.Store,
		SeriesLimiter: seriesLimiter,
		Admission:     admission,
	})

	// The API mounts under /api/; everything else is a page. One mux at the
	// root so exactly one middleware chain wraps the whole surface — a second
	// chain is a second place for a header to be forgotten.
	root := http.NewServeMux()
	root.Handle("/api/", apiMux)
	root.Handle("/", renderer.Routes())

	// The kit rides the public listener and its middleware chain deliberately:
	// same origin is what lets the site's CSP cover it unchanged. Registered
	// before the chain is built so it is inside it — a route outside the chain
	// is a route with no security headers and no rate limit.
	if opts.Config.DesignKit.Enabled() {
		kit, err := designkit.NewHandler(opts.Config.DesignKit.Dir)
		if err != nil {
			return nil, fmt.Errorf("server: design kit: %w", err)
		}
		// StripPrefix without the trailing slash, so the handler sees "/" for
		// the served root and can redirect to the kit's real entry point.
		root.Handle("/design-kit/", http.StripPrefix("/design-kit", kit))
	}

	chain := httpx.Chain{
		Resolver:          resolver,
		Limiter:           limiter,
		MaxBodyBytes:      maxBodyBytes,
		CSP:               opts.Config.Listen.CSP,
		PermissionsPolicy: opts.Config.Listen.PermissionsPolicy,
	}

	s := &Server{
		public: &http.Server{
			Addr:              opts.Config.Listen.Addr,
			Handler:           chain.Wrap(metrics.Instrument(root)),
			ReadHeaderTimeout: opts.Config.Timeouts.ReadHeader,
			ReadTimeout:       opts.Config.Timeouts.Read,
			WriteTimeout:      opts.Config.Timeouts.Write,
			IdleTimeout:       opts.Config.Timeouts.Idle,
			ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
		},
		private: &http.Server{
			Addr:              opts.Config.Listen.MetricsAddr,
			Handler:           privateMux(opts),
			ReadHeaderTimeout: opts.Config.Timeouts.ReadHeader,
			ReadTimeout:       opts.Config.Timeouts.Read,
			WriteTimeout:      opts.Config.Timeouts.Write,
			IdleTimeout:       opts.Config.Timeouts.Idle,
		},
		limiter:             limiter,
		breadth:             breadth,
		seriesLimiter:       seriesLimiter,
		log:                 opts.Logger,
		maxConns:            opts.Config.Listen.MaxConns,
		apiEvictInterval:    opts.Config.RateLimit.API.EvictInterval,
		seriesEvictInterval: opts.Config.RateLimit.Series.EvictInterval,
		shutdownGrace:       opts.Config.Timeouts.ShutdownGrace,
	}

	if opts.Config.Tiles.Enabled() {
		// Named origins, not "*": the tiles are on a different host, so every
		// fetch is cross-origin, and "*" would let any page on the internet
		// read them. listen.base_url leads and is not configurable here — the
		// site being unable to read its own basemap is not a state worth
		// letting an operator configure — and tiles.allowed_origins appends
		// whatever else may read it, normally a design or preview host.
		origins := append([]string{opts.Config.Listen.BaseURL}, opts.Config.Tiles.AllowedOrigins...)
		h, err := tiles.NewHandler(opts.Config.Tiles.Dir, opts.Config.Tiles.Archive, origins)
		if err != nil {
			return nil, fmt.Errorf("server: tiles: %w", err)
		}
		s.tiles = &http.Server{
			Addr:    opts.Config.Tiles.Addr,
			Handler: h,
			// The same timeouts as the other two. A range request for a few
			// kilobytes is not a slow request, and a client that cannot finish
			// one inside the write timeout is not a client we serve.
			ReadHeaderTimeout: opts.Config.Timeouts.ReadHeader,
			ReadTimeout:       opts.Config.Timeouts.Read,
			WriteTimeout:      opts.Config.Timeouts.Write,
			IdleTimeout:       opts.Config.Timeouts.Idle,
			ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
		}
	}
	return s, nil
}

func privateMux(opts Options) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Liveness, not readiness: the process is up. Readiness would depend on
		// the snapshot, and a restart loop caused by a slow first ingest is a
		// worse outcome than a page that says "data is not ready yet".
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	metricsHandler := metrics.Handler()
	mux.Handle("GET /metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if opts.Publisher != nil {
			opts.Publisher.ObserveAge(time.Now().UTC())
		}
		metricsHandler.ServeHTTP(w, r)
	}))

	return mux
}

// Run starts every configured listener and blocks until ctx is cancelled, then
// drains.
func (s *Server) Run(ctx context.Context) error {
	// Buffered for every listener that can send, so a goroutine whose listener
	// dies during shutdown never blocks forever on an unread channel.
	errCh := make(chan error, 3)

	go func() { errCh <- s.serveCapped(s.public) }()
	go func() { errCh <- listen(s.private) }()
	if s.tiles != nil {
		go func() { errCh <- s.serveCapped(s.tiles) }()
	}

	s.startEvicting(ctx)

	select {
	case <-ctx.Done():
		return s.shutdown()
	case err := <-errCh:
		if err != nil {
			// One listener dying must bring the other down too, rather than
			// leaving a half-serving process that a health check calls healthy.
			_ = s.shutdown()
			return err
		}
		// A nil error means that listener returned ErrServerClosed, which only
		// happens once shutdown is already under way.
		return s.shutdown()
	}
}

// startEvicting starts each limiter's sweep goroutine. Sweeping the limiter and
// breadth maps is what keeps them bounded: without it, the defence against
// memory exhaustion is itself the leak. Every StartEvicting call stops when ctx
// is cancelled.
//
// Each limiter sweeps on ITS OWN configured interval. They were once all driven
// by ratelimit.api.evict_interval, which made ratelimit.series.evict_interval
// dead — invisible for as long as the two values happened to be equal in
// airbg.yaml, and silence for an operator who tightened the series one.
//
// The breadth limiter is the exception, and deliberately: ratelimit.enumerate
// has no evict_interval key of its own, so it shares the API interval. If a
// key is ever added there, this call must use it. Documented in
// docs/configuration.md §10 as well — an undocumented shared value is how the
// series bug happened in the first place.
func (s *Server) startEvicting(ctx context.Context) {
	s.limiter.StartEvicting(ctx, s.apiEvictInterval)
	s.breadth.StartEvicting(ctx, s.apiEvictInterval)
	s.seriesLimiter.StartEvicting(ctx, s.seriesEvictInterval)
}

// serveCapped listens and serves srv under the connection cap.
//
// Separate from listen() because it is the two internet-facing listeners that
// are capped, not the private one: /metrics and /healthz sit on loopback, and
// capping them would mean a connection flood could also blind the operator to
// the flood.
//
// The tiles listener needs this at least as much as the public one does. File
// descriptors and goroutines are process-wide, so an unbounded tiles listener
// exhausts them and takes the public listener's Accept down with it — the one
// resource the tiles bulkhead cannot separate, having separated the pool, the
// snapshot, the limiters and the admission semaphore. And the assumption that
// makes the cap look redundant on the public listener — that the origin is
// reachable only through Cloudflare — is known false for tiles by design: that
// port is on a DNS-only hostname and accepts the world.
//
// Both take listen.max_conns rather than the tiles listener taking a key of its
// own. A tiles.max_conns would have to ship alongside its all-or-nothing
// string neighbours, and an int cannot ship "empty": it would ship 0, which
// LimitListener treats as no limiting — the shipped configuration would then
// carry exactly the defect this closes. One number bounding what any one
// internet-facing socket may hold is also the shape of the resource being
// bounded, which is per-process, not per-workload. The cost is real and
// accepted: dozens of range requests per map load and one JSON request per page
// are different workloads sharing a knob, so an operator who raises the cap for
// tiles raises it for the API too.
func (s *Server) serveCapped(srv *http.Server) error {
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", srv.Addr, err)
	}
	if err := srv.Serve(httpx.LimitListener(ln, int(s.maxConns))); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving %s: %w", srv.Addr, err)
	}
	return nil
}

func listen(srv *http.Server) error {
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listening on %s: %w", srv.Addr, err)
	}
	return nil
}

func (s *Server) shutdown() error {
	// A fresh context: ctx is already cancelled, and passing it would make
	// Shutdown return immediately and kill in-flight requests — the opposite of
	// graceful.
	ctx, cancel := context.WithTimeout(context.Background(), s.shutdownGrace)
	defer cancel()

	s.log.Info("shutting down")
	err := s.public.Shutdown(ctx)
	if perr := s.private.Shutdown(ctx); err == nil {
		err = perr
	}
	if s.tiles != nil {
		if terr := s.tiles.Shutdown(ctx); err == nil {
			err = terr
		}
	}
	return err
}
