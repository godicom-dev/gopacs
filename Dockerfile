FROM golang:1.26.4-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/gopacs ./cmd/gopacs

FROM debian:bookworm-slim

RUN mkdir -p /var/data
COPY --from=build /out/gopacs /usr/local/bin/gopacs

EXPOSE 10000
# 64 MiB bodies fit a 512 MB instance. Raise -max-upload with the plan.
CMD ["gopacs", "-data", "/var/data", "-max-upload", "67108864"]
