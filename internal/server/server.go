// Package server assembles the two listeners.
//
// Two, not one: the public listener carries the middleware chain and the
// public routes; the private listener carries /metrics and /healthz. Separate
// listeners rather than a path prefix, because a prefix is one routing mistake
// away from exposing the counters that tell a scraper whether it is being
// throttled.
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
	"airbg.org/internal/httpx"
	"airbg.org/internal/i18n"
	"airbg.org/internal/metrics"
	"airbg.org/internal/ratelimit"
	"airbg.org/internal/snapshot"
	"airbg.org/internal/web"
)

type Options struct {
	ListenAddr  string
	MetricsAddr string

	Catalogue *i18n.Catalogue
	Snapshots *snapshot.Holder
	Store     api.DataSource
	Publisher *Publisher

	TrustedProxyCIDRs []string
	BaseURL           string
	Logger            *slog.Logger

	// BasemapStyleURL is the MapLibre style JSON URL, key already substituted,
	// or empty when no basemap vendor is configured. See config.Config.BasemapStyleURL.
	BasemapStyleURL string

	// CSP is the policy the public chain's SecurityHeaders sets. Built by the
	// caller (main, via httpx.CSP(cfg.BasemapHost)) rather than here, so this
	// package does not need to know how a policy is assembled — that stays in
	// one place, visible at the wiring.
	CSP string

	// MaxDBInflight bounds how many requests may be inside a database query at
	// once, across every client. See internal/admit and config.MaxDBInflight.
	MaxDBInflight int32

	// MaxConns bounds how many connections the public listener holds open at
	// once. See internal/httpx.LimitListener and config.MaxConns.
	MaxConns int32
}

type Server struct {
	public        *http.Server
	private       *http.Server
	limiter       *ratelimit.Limiter
	breadth       *ratelimit.Breadth
	seriesLimiter *ratelimit.Limiter
	log           *slog.Logger
	maxConns      int32
}

// Timeouts. Every one of these is a bound on what a single connection can cost.
const (
	// readHeaderTimeout is the slowloris bound: a connection that has not sent
	// a complete request line and headers by then is closed.
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownGrace     = 15 * time.Second

	// evictInterval sweeps the rate-limit and breadth maps.
	evictInterval = 5 * time.Minute

	// maxBodyBytes: this service answers GETs. Anything larger than a
	// generously sized header block is not a request we serve.
	maxBodyBytes = 64 << 10

	// defaultMaxDBInflight is admit.DefaultSize, the one definition shared with
	// config and api. Used only when Options.MaxDBInflight is zero — see the
	// comment at its one call site.
	defaultMaxDBInflight int32 = admit.DefaultSize

	// defaultMaxConns matches config.defaultMaxConns. Used only when
	// Options.MaxConns is zero — see the comment at its one call site.
	defaultMaxConns int32 = 4096
)

// The rate limit. Deliberately generous for a human reading the map — a page
// load fans out to several API calls — and tight enough that a scraper walking
// every area hits it long before it finishes.
//
// One limit for the whole public surface, not one per route: separate buckets
// would let a client spend its full budget on every route in turn, so the real
// ceiling would be the sum, which is not the number anyone reasoned about.
var apiRate = ratelimit.Rate{PerSecond: 10, Burst: 60}

// bucketTTL is how long an idle client's bucket is kept. Long enough that a
// reader who steps away and comes back is still throttled on their old bucket
// rather than handed a fresh burst; short enough that the map stays bounded.
const bucketTTL = 30 * time.Minute

func New(opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Catalogue == nil || opts.Snapshots == nil {
		return nil, errors.New("server: Catalogue and Snapshots are required")
	}

	resolver, err := httpx.NewIPResolver(opts.TrustedProxyCIDRs)
	if err != nil {
		return nil, fmt.Errorf("server: trusted proxies: %w", err)
	}

	renderer, err := web.NewRenderer(opts.Catalogue, opts.Snapshots, opts.BaseURL, opts.BasemapStyleURL)
	if err != nil {
		return nil, fmt.Errorf("server: renderer: %w", err)
	}

	limiter := ratelimit.New(apiRate, bucketTTL)
	seriesLimiter := api.NewSeriesLimiter()
	breadth := ratelimit.NewBreadth(
		ratelimit.DistinctAreaLimit,
		ratelimit.DistinctSensorLimit,
		ratelimit.EnumerationWindow,
	)

	// Built here, not left for api.NewRouter's fail-closed default, so its size
	// is the operator's configured value rather than the package-level fallback.
	//
	// A zero Options.MaxDBInflight means "not set" rather than "admit nothing":
	// config.Load always supplies a positive value in production, but tests
	// (and any other caller that builds Options by hand) commonly omit it, and
	// silently refusing every database-backed request would be a surprising way
	// to find that out.
	maxInflight := opts.MaxDBInflight
	if maxInflight <= 0 {
		maxInflight = defaultMaxDBInflight
	}
	admission, err := admit.New(int(maxInflight))
	if err != nil {
		return nil, fmt.Errorf("server: admission: %w", err)
	}

	// Zero means "not set" — the same convention as maxInflight above — but
	// == 0 rather than <= 0: a negative value is a configuration mistake, and
	// falling through to the error path is the honest response. config.Load
	// never lets a negative value reach here in production; == 0 keeps that
	// error path alive for any other caller that builds Options by hand.
	maxConns := opts.MaxConns
	if maxConns == 0 {
		maxConns = defaultMaxConns
	}

	apiMux := api.NewRouter(api.Deps{
		Snapshots:     opts.Snapshots,
		Breadth:       breadth,
		Store:         opts.Store,
		BaseURL:       opts.BaseURL,
		SeriesLimiter: seriesLimiter,
		Admission:     admission,
	})

	// The API mounts under /api/; everything else is a page. One mux at the
	// root so exactly one middleware chain wraps the whole surface — a second
	// chain is a second place for a header to be forgotten.
	root := http.NewServeMux()
	root.Handle("/api/", apiMux)
	root.Handle("/", renderer.Routes())

	chain := httpx.Chain{
		Resolver:     resolver,
		Limiter:      limiter,
		MaxBodyBytes: maxBodyBytes,
		CSP:          opts.CSP,
	}

	s := &Server{
		public: &http.Server{
			Addr:              opts.ListenAddr,
			Handler:           chain.Wrap(metrics.Instrument(root)),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
		},
		private: &http.Server{
			Addr:              opts.MetricsAddr,
			Handler:           privateMux(opts),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
		},
		limiter:       limiter,
		breadth:       breadth,
		seriesLimiter: seriesLimiter,
		log:           opts.Logger,
		maxConns:      maxConns,
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

// Run starts both listeners and blocks until ctx is cancelled, then drains.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 2)

	go func() { errCh <- s.servePublic() }()
	go func() { errCh <- listen(s.private) }()

	// Sweeping the limiter and breadth maps is what keeps them bounded. Without
	// it the defence against memory exhaustion is itself the leak. Both
	// StartEvicting calls stop when ctx is cancelled.
	s.limiter.StartEvicting(ctx, evictInterval)
	s.breadth.StartEvicting(ctx, evictInterval)
	s.seriesLimiter.StartEvicting(ctx, evictInterval)

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

// servePublic listens and serves the public server under the connection cap.
//
// Separate from listen() because only the public listener is capped: the private
// listener carries /metrics and /healthz on loopback, and capping it would mean a
// connection flood could also blind the operator to the flood.
func (s *Server) servePublic() error {
	ln, err := net.Listen("tcp", s.public.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.public.Addr, err)
	}
	if err := s.public.Serve(httpx.LimitListener(ln, int(s.maxConns))); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving %s: %w", s.public.Addr, err)
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
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	s.log.Info("shutting down")
	err := s.public.Shutdown(ctx)
	if perr := s.private.Shutdown(ctx); err == nil {
		err = perr
	}
	return err
}
