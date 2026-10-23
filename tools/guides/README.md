# Guides

Tools for the README's GIFs: they record real berthlight sessions and
render them as the terminal would show them, Kitty graphics included
(the usual tools render in a browser terminal, which has none).

```sh
# on the host, with Docker or Podman, berthlight's proxy port 7480 free
go run ./cmd/brag -berthlight /path/to/berthlight -out brag.cast
go run ./cmd/gif -in brag.cast -out ../../docs/brag.gif
```

- `cmd/brag` runs `berthlight status --watch` in a pseudo-terminal, with
  Ghostty's environment, while twelve dev servers come up in their own
  containers, and writes an [asciicast v2](https://docs.asciinema.org/manual/asciicast/v2/)
  recording. Everything it starts is removed afterwards.
- `cmd/gif` plays a recording through `term`, a small terminal emulator
  that understands what berthlight writes (colours, cursor movement, the
  title, notifications, Kitty graphics), and writes a GIF in Go Mono with
  one palette and only the changed part of each frame.

A separate module, so berthlight itself takes none of its dependencies.
