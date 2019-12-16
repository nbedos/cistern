FROM golang:1.13 AS builder
WORKDIR /citop/
COPY . .
RUN apt-get update && yes | apt-get install pandoc
RUN make citop

FROM debian:latest
RUN apt-get update && yes | apt-get install ca-certificates less man
COPY --from=builder /citop/build/citop /bin
CMD ["-r", "github.com/nbedos/citop", "master"]
ENTRYPOINT ["citop"]
