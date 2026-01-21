# LPD8 LED Bridge

A Go program that provides LED feedback for the Akai LPD8 MK2 by tracking button presses and controlling the RGB LEDs via SysEx messages.

**Part of the [Serato Stem Control](../README.md) project.**

## How It Works

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────┐
│   LPD8 MK2  │────▶│  LPD8 LED Bridge │────▶│  LPD8 LEDs  │
│  (buttons)  │     │                  │     │  (SysEx)    │
└─────────────┘     │   Local State    │     └─────────────┘
                    │    Tracking      │
┌─────────────┐     │                  │
│ PLX-CRSS12  │────▶│  (optional spy)  │
│  (buttons)  │     └──────────────────┘
└─────────────┘
```

### Local State Tracking

This bridge **does not intercept MIDI messages from Serato**. Instead, it tracks button presses locally:

- When you press a pad on the LPD8, the bridge updates the LED state
- When you press a stem button on a connected turntable (via `-spy`), the bridge mirrors that state
- The bridge and Serato both receive the same button press, keeping them in sync

**What this means:**
- If you activate stems **from within Serato's UI** (mouse/keyboard), the LPD8 LEDs won't update
- If LEDs get out of sync, simply **restart the bridge** to reset to the default state

This design avoids MIDI feedback loops and works reliably with Serato's MIDI implementation.

## Installation

### Pre-built Binaries

Download from the [Releases](../../releases) page:

- `lpd8-led-bridge-darwin-arm64` - macOS Apple Silicon
- `lpd8-led-bridge-darwin-amd64` - macOS Intel
- `lpd8-led-bridge-windows-amd64.exe` - Windows 64-bit
- `lpd8-led-bridge-windows-386.exe` - Windows 32-bit

### From Source

**Requirements:**
- Go 1.22 or later
- C compiler (for rtmidi CGO dependencies)
  - macOS: Xcode Command Line Tools (`xcode-select --install`)
  - Windows: MinGW-w64 or MSYS2
  - Linux: `build-essential` package

```bash
go build -o lpd8-led-bridge .

# Or use the build script
./build.sh v1.0.0
```

## Usage

```bash
# List available MIDI ports
./lpd8-led-bridge -list

# Run with LPD8 output
./lpd8-led-bridge -out "LPD8 mk2"

# Run with turntable button mirroring
./lpd8-led-bridge -out "LPD8 mk2" -spy "PLX-CRSS12"

# With custom config
./lpd8-led-bridge -out "LPD8 mk2" -config config.json
```

### Command Line Options

| Option | Description |
|--------|-------------|
| `-out "PORT"` | MIDI output port for LPD8 (required) |
| `-spy "PORT"` | MIDI input to mirror button presses from |
| `-config FILE` | Load configuration from JSON file |
| `-genconfig FILE` | Generate default config file and exit |
| `-list` | List available MIDI ports |
| `-test` | Test LED colors |
| `-debug` | Enable verbose debug logging |

## LED Behavior

```
┌─────┬─────┬─────┬─────┐
│  5  │  6  │  7  │  8  │  ← Top row (Blue) - Stem On/Off
│ NT40│ NT41│ NT42│ NT43│
├─────┼─────┼─────┼─────┤
│  1  │  2  │  3  │  4  │  ← Bottom row (Amber) - Stem FX
│ NT36│ NT37│ NT38│ NT39│
└─────┴─────┴─────┴─────┘
```

| Action | Result |
|--------|--------|
| **Startup** | Top row (blue) ON, bottom row OFF |
| **Press amber pad** | Amber ON, controlled blues OFF |
| **Press amber again** | Amber OFF, controlled blues ON |
| **Press blue pad** | Toggle blue, turn off controlling ambers |
| **Knob to 0** | Corresponding blue turns OFF |
| **Knob above 2** | Corresponding blue turns ON (brightness scales with value) |

### Default Control Mappings

- Pad 1 (amber) controls Pad 5 (blue)
- Pad 2 (amber) controls Pads 6, 7, 8 (blue)
- Pad 3 (amber) controls Pads 6, 7, 8 (blue)
- Pad 4 (amber) controls Pad 8 (blue)

## Configuration

Generate a default config with `-genconfig config.json`:

```json
{
  "lpd8": {
    "top_row": [40, 41, 42, 43],
    "bottom_row": [36, 37, 38, 39],
    "knobs": [70, 71, 72, 73, 74, 75, 76, 77],
    "channel": 10,
    "knob_channel": 0
  },
  "spy_remap": {
    "32": 40, "33": 41, "34": 42, "35": 43
  },
  "amber_to_blues": {
    "36": [40],
    "37": [41, 42, 43],
    "38": [41, 42, 43],
    "39": [43]
  },
  "knob_to_blue": {
    "70": 40, "71": 41, "72": 42, "73": 43
  }
}
```

### Config Fields

| Field | Description |
|-------|-------------|
| `lpd8.top_row` | MIDI notes for top row pads (blue LEDs) |
| `lpd8.bottom_row` | MIDI notes for bottom row pads (amber LEDs) |
| `lpd8.knobs` | CC numbers for knobs 1-8 |
| `lpd8.channel` | MIDI channel for pads (1-16) |
| `lpd8.knob_channel` | MIDI channel for knobs (0 = all channels) |
| `spy_remap` | Map spy device notes to LPD8 notes |
| `amber_to_blues` | Which blues each amber controls |
| `knob_to_blue` | Which blue each knob controls |

## Troubleshooting

### LEDs out of sync with Serato

Restart the bridge to reset to default state:
```bash
# Ctrl+C to stop, then restart
./lpd8-led-bridge -out "LPD8 mk2"
```

### No MIDI ports found

- Ensure the LPD8 is connected and powered on
- Check that no other application has exclusive access to the MIDI port
- On macOS, you may need to enable the IAC Driver in Audio MIDI Setup

### LEDs not responding

- Verify the port name matches exactly (use `-list` to check)
- Test with `-test` to cycle through colors
- Ensure your LPD8 is in the correct program/preset

### Wrong pads lighting up

- The LPD8's pad notes may differ from defaults if reprogrammed
- Use a MIDI monitor to check what notes your LPD8 sends
- Update the config file to match your LPD8's programming

### Debugging

```bash
./lpd8-led-bridge -out "LPD8 mk2" -debug
```

This shows verbose logging of pad presses, knob changes, and LED state changes.

## Building Releases

Due to CGO dependencies (rtmidi), cross-compilation requires building on each target platform:

```bash
# On macOS ARM64 (Apple Silicon)
./build.sh v1.0.0
# Creates: releases/lpd8-led-bridge-darwin-arm64

# On macOS AMD64 (Intel) or via Rosetta
arch -x86_64 ./build.sh v1.0.0
# Creates: releases/lpd8-led-bridge-darwin-amd64

# On Windows
./build.sh v1.0.0
# Creates: releases/lpd8-led-bridge-windows-amd64.exe
```

Upload releases to GitHub:
```bash
gh release create v1.0.0 releases/* --title "v1.0.0" --notes "Release notes here"
```

## License

MIT License
