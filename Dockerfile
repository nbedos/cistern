FROM golang:1.13
WORKDIR /citop/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o citop .

FROM debian:latest
WORKDIR /citop/
COPY . .
RUN apt-get update && yes | apt-get install ca-certificates less
COPY --from=0 /citop/citop /bin
CMD ["citop"]
