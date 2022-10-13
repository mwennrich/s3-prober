FROM golang:1.19 AS builder
ENV GO111MODULE=on
ENV CGO_ENABLED=0


COPY / /work
WORKDIR /work

RUN make s3-prober

FROM scratch
COPY --from=builder /work/bin/s3-prober /s3-prober
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

USER 999
ENTRYPOINT ["/s3-prober"]
