FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/airbg ./cmd/airbg

# Distroless: no shell, no package manager, no writable document root. Nothing
# dropped into the container can be executed the way anything in the legacy
# www-root/ could be (spec §4.1).
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/airbg /airbg
USER nonroot:nonroot
ENTRYPOINT ["/airbg"]
CMD ["collect"]
