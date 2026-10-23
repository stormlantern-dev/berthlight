# The proxy image: the same berthlight binary, running `berthlight proxy`.
# Static, no shell, non-root (distroless's nonroot user, 65532).
FROM docker.io/library/golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=""
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /berthlight ./cmd/berthlight

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /berthlight /berthlight
USER 65532:65532
EXPOSE 7480
ENTRYPOINT ["/berthlight", "proxy"]
CMD ["--state", "/state"]
