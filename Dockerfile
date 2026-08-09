FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/airbg ./cmd/airbg

# Distroless: no shell, no package manager, no writable document root. Nothing
# dropped into the container can be executed the way anything in the legacy
# www-root/ could be (spec §4.1).
# debian13 (trixie) is current stable; debian12 is oldstable. The binary is
# statically linked (CGO_ENABLED=0), so the base contributes only CA
# certificates, /etc/passwd and timezone data — but those still carry CVE
# fixes, so track the current major rather than the one that was current when
# this was written.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/airbg /airbg
USER nonroot:nonroot
ENTRYPOINT ["/airbg"]
CMD ["collect"]
