# syntax=docker/dockerfile:1.7

# ---- build stage --------------------------------------------------------
FROM golang:1.24-alpine AS build

WORKDIR /src

# Cache the module graph first so dependency changes don't re-trigger the
# source copy + compile when only Go files change.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Static binary, stripped, no CGO. Build the server cmd only.
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/server \
    ./cmd/server

# ---- runtime stage ------------------------------------------------------
# distroless/static is ~2MB and ships only the static glibc-less base.
# nonroot user is uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/server /server

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/server"]
