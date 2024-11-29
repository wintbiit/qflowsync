FROM golang:1.22-alpine as builder

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN go build -trimpath -ldflags "-s -w" -o /app/main

FROM alpine:3.12

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/main /usr/local/bin/main

VOLUME /app

ENTRYPOINT ["/usr/local/bin/main"]