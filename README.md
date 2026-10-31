# thumbmix-mcp

An MCP server (stdio) that lets Claude Code read and change a Midas M32 mixing desk over OSC/UDP, in
natural language: "more bass on the bass guitar", "more compression on Headset 1". A companion to
[Thumbmix](https://github.com/Auxority/thumbmix), the iPhone remote for the M32. It is small on purpose.

## Build and add

```sh
go build -o thumbmix-mcp .
claude mcp add --transport stdio thumbmix -- /path/to/thumbmix-mcp --host <desk IP>
```

`thumbmix-mcp --help` lists the flags: `--host` (required), `--port` (default 10023), `--timeout` (default 1s).
There is no authentication: the desk's network (VLAN) is the access control. Writes are always on.

## Tools

| Tool | What it does |
| --- | --- |
| `list_strips` | Every strip (inputs, aux ins, FX returns, buses, matrices, mains, DCAs): id, desk name, linked partner |
| `get_strip` | Fader dB, mute, pan, EQ bands, comp, and the preamp (gain, 48V) where one feeds the strip |
| `set_fader` | Fader level in dB, -90 (down) to +10 |
| `set_mute` | Mute or un-mute |
| `set_eq_band` | Band gain (-15..15 dB), frequency (20..20000 Hz), Q (0.3..10) |
| `set_comp` | Threshold, ratio, makeup, attack, release, on/off |

A strip is its id (`ch/01`, `bus/03`, `main/st`, `dca/2`) or its desk name; case and accents are ignored,
an exact name wins, and an ambiguous name is refused with the candidates. Preamp gain and 48V can be read but
have no write tool.

## Safety

- Every argument is checked: a wrong type, an unknown field or an out-of-range value is refused, never clamped.
- Before a write the server reads the current value from the desk. If the desk does not answer within the
  timeout, it refuses with "desk not answering" and sends nothing.
- After a write it reads the value back and reports `{from, to}`. The desk snaps values to its own steps,
  so `to` can differ slightly from what was asked.
- A value the desk did not answer is `null`, never a guess.
- A linked pair (`linked_with`) may move together on the desk; the result says so.

Addresses, ranges and value laws are ported from Thumbmix, which checks them against Patrick-Gilles Maillot's
*Unofficial X32/M32 OSC Remote Protocol* v4.09; the code names the Thumbmix file where a conversion comes from.

## Try it without a desk

Thumbmix has a fake console. From a Thumbmix checkout: `swift run --package-path Core fake-m32` (listens on
127.0.0.1:10023), then `thumbmix-mcp --host 127.0.0.1`.

## Tests

`go test ./...` runs the OSC, unit-conversion and name tests, and end-to-end tests against a tiny UDP fake desk,
including an MCP round trip over the SDK's in-memory transport.

MIT license.
