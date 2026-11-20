# The proxy image: the same berthlight binary, running `berthlight proxy`.
# Static, no shell, non-root (distroless's nonroot user, 65532).
FROM docker.io/library/golang:1.27@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=""
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /berthlight ./cmd/berthlight

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /berthlight /berthlight
USER 65532:65532
EXPOSE 7480
ENTRYPOINT ["/berthlight", "proxy"]
CMD ["--state", "/state"]
