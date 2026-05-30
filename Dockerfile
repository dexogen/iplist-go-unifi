FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/iplist-go-unifi ./cmd/iplist-go-unifi

FROM alpine:3.22
RUN addgroup -S iplist && adduser -S -G iplist iplist
COPY --from=build /out/iplist-go-unifi /usr/local/bin/iplist-go-unifi
USER iplist
EXPOSE 18086
ENTRYPOINT ["/usr/local/bin/iplist-go-unifi"]
