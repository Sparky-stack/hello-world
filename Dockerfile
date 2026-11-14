# syntax=docker/dockerfile:1

# Build stage: static, reproducible Go binary.
FROM docker.io/library/golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/hello-world .

# Runtime stage: distroless static, non-root (uid 65532).
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /out/hello-world /hello-world
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/hello-world"]
