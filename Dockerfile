FROM golang:1.27.3-trixie@sha256:fbb7ae40b62ff8afe31662df570d39b4f5052ac32b90dae8e60d7657a20a74f4 AS build

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