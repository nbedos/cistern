FROM golang:1.13 AS builder
WORKDIR /citop/
RUN apt-get update && yes | apt-get install pandoc
COPY . .
RUN make citop

FROM alpine:latest
WORKDIR /citop
RUN apk add man ca-certificates less
ENV PAGER less
COPY --from=builder /citop/build/citop /bin
CMD []
ENTRYPOINT ["citop"]
