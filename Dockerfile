# Build-only: compiles the helper and packs the extension zip.
# An extension runs inside gnome-shell, so there is no runtime container.
# Export the zip with: docker build --output dist .   (or podman build --output dist .)
ARG VERSION=0.0.1

FROM golang:1.27 AS helper
ARG VERSION
WORKDIR /src
COPY helper/ helper/
RUN cd helper && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/ptb-helper ./cmd/ptb-helper

FROM fedora:latest AS pack
RUN dnf install -y zip glib2-devel && dnf clean all
WORKDIR /ext
COPY extension/ .
RUN rm -rf test
COPY --from=helper /out/ptb-helper bin/ptb-helper
RUN glib-compile-schemas schemas \
    && mkdir -p /dist \
    && zip -qr /dist/poketokenbar@local.zip .

FROM scratch
COPY --from=pack /dist/ /
