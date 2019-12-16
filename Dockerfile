FROM golang:1.13 AS builder
WORKDIR /citop/
RUN apt-get update && yes | apt-get install pandoc
COPY . .
RUN make citop

FROM alpine:latest
RUN apk add man
COPY --from=builder /citop/build/citop /bin
CMD ["-r", "github.com/nbedos/citop", "master"]
ENTRYPOINT ["citop"]
