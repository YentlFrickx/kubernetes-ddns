FROM golang:alpine as builder

WORKDIR /app

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" .

FROM alpine:3.14 AS final

WORKDIR /app

COPY --from=builder /app/kubernetes-ddns /usr/bin/

ENTRYPOINT ["kubernetes-ddns"]