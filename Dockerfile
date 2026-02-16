FROM golang:1.26 AS builder
ENV GO111MODULE=on
ENV CGO_ENABLED=0


COPY / /work
WORKDIR /work

RUN make s3-prober

FROM gcr.io/distroless/static
COPY --from=builder /work/s3-prober /s3-prober

USER 999
ENTRYPOINT ["/s3-prober"]
