FROM golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG VCS_REF=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -mod=readonly \
    -trimpath \
    -buildvcs=false \
    -ldflags="-s -w -buildid= -X main.buildVersion=${VERSION} -X main.buildCommit=${VCS_REF}" \
    -o /out/joshbot \
    ./cmd/joshbot

FROM scratch

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/joshbot /joshbot

USER 65532:65532

ENTRYPOINT ["/joshbot"]
CMD ["help"]