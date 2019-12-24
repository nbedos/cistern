FROM golang:1.13 AS builder
WORKDIR /cistern/
RUN apt-get update && yes | apt-get install pandoc
COPY . .
RUN go run ./cmd/make cistern

FROM alpine:latest
WORKDIR /cistern
RUN apk add man ca-certificates less
ENV PAGER less
COPY --from=builder /cistern/build/cistern /bin
CMD []
ENTRYPOINT ["cistern"]
