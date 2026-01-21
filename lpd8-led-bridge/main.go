package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"gitlab.com/gomidi/midi/v2"
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"
)

// Config defines the button/knob mappings
type Config struct {
	// LPD8 pad notes (physical layout: top row 5-8, bottom row 1-4)
	LPD8 struct {
		TopRow       [4]int `json:"top_row"`       // Blue pads (default: 40,41,42,43)
		BottomRow    [4]int `json:"bottom_row"`    // Amber pads (default: 36,37,38,39)
		Knobs        [8]int `json:"knobs"`         // CC numbers for knobs 1-8
		Channel      int    `json:"channel"`       // MIDI channel for pads (1-16, default: 10)
		KnobChannel  int    `json:"knob_channel"`  // MIDI channel for knobs (0=all, 1-16, default: 0)
	} `json:"lpd8"`

	// Spy device note remapping (e.g., PLX-CRSS12)
	SpyRemap map[string]int `json:"spy_remap"` // "32": 40 means spy note 32 -> our note 40

	// Control mappings: which amber controls which blues
	// Key is amber note, value is list of blue notes it controls
	AmberToBlues map[string][]int `json:"amber_to_blues"`

	// Knob to blue mapping: which CC controls which blue LED
	// When knob value is 0, blue turns off; when > 3, blue turns on
	KnobToBlue map[string]int `json:"knob_to_blue"`
}

// Default configuration
func defaultConfig() Config {
	cfg := Config{}
	cfg.LPD8.TopRow = [4]int{40, 41, 42, 43}
	cfg.LPD8.BottomRow = [4]int{36, 37, 38, 39}
	cfg.LPD8.Knobs = [8]int{70, 71, 72, 73, 74, 75, 76, 77}
	cfg.LPD8.Channel = 10
	cfg.LPD8.KnobChannel = 0 // 0 = accept all channels (global)

	cfg.SpyRemap = map[string]int{
		"32": 40, "33": 41, "34": 42, "35": 43,
	}

	cfg.AmberToBlues = map[string][]int{
		"36": {40},           // Pad 1 controls Pad 5
		"37": {41, 42, 43},   // Pad 2 controls Pads 6, 7, 8
		"38": {41, 42, 43},   // Pad 3 controls Pads 6, 7, 8
		"39": {43},           // Pad 4 controls Pad 8
	}

	cfg.KnobToBlue = map[string]int{
		"70": 40, // Knob 1 (CC 70) controls blue pad 5 (note 40)
		"71": 41, // Knob 2 (CC 71) controls blue pad 6 (note 41)
		"72": 42, // Knob 3 (CC 72) controls blue pad 7 (note 42)
		"73": 43, // Knob 4 (CC 73) controls blue pad 8 (note 43)
	}

	return cfg
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func saveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Build runtime mappings from config
func buildMappings(cfg Config) {
	// Clear and rebuild noteToPayloadPos
	noteToPayloadPos = make(map[uint8]int)
	for i, note := range cfg.LPD8.TopRow {
		noteToPayloadPos[uint8(note)] = i + 4 // Top row = SysEx positions 4-7
	}
	for i, note := range cfg.LPD8.BottomRow {
		noteToPayloadPos[uint8(note)] = i // Bottom row = SysEx positions 0-3
	}

	// Rebuild isTopRow
	isTopRow = make(map[uint8]bool)
	for _, note := range cfg.LPD8.TopRow {
		isTopRow[uint8(note)] = true
	}
	for _, note := range cfg.LPD8.BottomRow {
		isTopRow[uint8(note)] = false
	}

	// Rebuild amberToBlues from config
	amberToBlues = make(map[uint8][]uint8)
	for noteStr, blues := range cfg.AmberToBlues {
		var note int
		fmt.Sscanf(noteStr, "%d", &note)
		bluesU8 := make([]uint8, len(blues))
		for i, b := range blues {
			bluesU8[i] = uint8(b)
		}
		amberToBlues[uint8(note)] = bluesU8
	}

	// Rebuild blueToAmbers (reverse mapping)
	blueToAmbers = make(map[uint8][]uint8)
	for amber, blues := range amberToBlues {
		for _, blue := range blues {
			blueToAmbers[blue] = append(blueToAmbers[blue], amber)
		}
	}

	// Rebuild crss12NoteRemap
	crss12NoteRemap = make(map[uint8]uint8)
	for noteStr, mapped := range cfg.SpyRemap {
		var note int
		fmt.Sscanf(noteStr, "%d", &note)
		crss12NoteRemap[uint8(note)] = uint8(mapped)
	}

	// Rebuild knobToBlue
	knobToBlue = make(map[uint8]uint8)
	for ccStr, blueNote := range cfg.KnobToBlue {
		var cc int
		fmt.Sscanf(ccStr, "%d", &cc)
		knobToBlue[uint8(cc)] = uint8(blueNote)
	}

	// Store channels (convert 1-16 to 0-15, 0 stays 0 for "all")
	lpd8Channel = uint8(cfg.LPD8.Channel - 1)
	if cfg.LPD8.KnobChannel == 0 {
		lpd8KnobChannel = 255 // Special value meaning "accept all channels"
	} else {
		lpd8KnobChannel = uint8(cfg.LPD8.KnobChannel - 1)
	}
}

var lpd8Channel uint8 = 9       // Default channel 10 (0-indexed) for pads
var lpd8KnobChannel uint8 = 255 // Default: accept all channels for knobs
var debugMode bool = false      // Debug logging

func debugLog(format string, v ...interface{}) {
	if debugMode {
		log.Printf(format, v...)
	}
}

// LPD8 MK2 SysEx for LED control
// Format: F0 47 7F 4C 06 00 30 [48 bytes] F7
// Product ID = 0x4C (not 0x30)
// Each color channel is 2 bytes: [high=0x00, low=value]
// So each pad = 6 bytes, 8 pads = 48 bytes
var sysExHeader = []byte{0xF0, 0x47, 0x7F, 0x4C, 0x06, 0x00, 0x30}
var sysExFooter = []byte{0xF7}

// Pad colors (RGB values 0-127)
type Color struct {
	R, G, B byte
}

var (
	colorOff       = Color{0, 0, 0}       // LED off (black)
	colorTopRow    = Color{0, 0, 127}     // Blue for top row (stem on/off)
	colorBottomRow = Color{127, 40, 0}    // Amber for bottom row (FX)
)

// Runtime mappings (rebuilt from config)
var noteToPayloadPos = map[uint8]int{}
var isTopRow = map[uint8]bool{}
var amberToBlues = map[uint8][]uint8{}
var blueToAmbers = map[uint8][]uint8{}
var crss12NoteRemap = map[uint8]uint8{}
var knobToBlue = map[uint8]uint8{} // CC number -> blue note


// Current LED colors for each pad position
var padColors [8]Color

// Track toggle state for each pad (true = LED on with color, false = LED off)
var padState = make(map[uint8]bool)
var stateMutex sync.Mutex

// Global send function (set after opening output port)
var sendSysEx func([]byte) error

// Build payload (48 bytes: 6 per pad)
// Each color channel is 2 bytes: [high=0x00, low=value]
func buildPayload(colors [8]Color) []byte {
	payload := make([]byte, 0, 48)
	for _, c := range colors {
		// R: high byte (always 0), low byte (value)
		payload = append(payload, 0x00, c.R)
		// G: high byte (always 0), low byte (value)
		payload = append(payload, 0x00, c.G)
		// B: high byte (always 0), low byte (value)
		payload = append(payload, 0x00, c.B)
	}
	return payload
}

// Build complete SysEx message
func buildSysEx(colors [8]Color) []byte {
	payload := buildPayload(colors)
	msg := make([]byte, 0, 64)
	msg = append(msg, sysExHeader...)
	msg = append(msg, payload...)
	msg = append(msg, sysExFooter...)
	return msg
}

// Toggle a pad's LED state and send update
func togglePad(note uint8) {
	stateMutex.Lock()
	defer stateMutex.Unlock()

	pos, ok := noteToPayloadPos[note]
	if !ok {
		return
	}

	// Toggle the state
	padState[note] = !padState[note]
	isOn := padState[note]

	// Determine color based on state and row
	var newColor Color
	var colorName string
	if isOn {
		if isTopRow[note] {
			newColor = colorTopRow // Blue
			colorName = "BLUE"
		} else {
			newColor = colorBottomRow // Amber
			colorName = "AMBER"
		}
	} else {
		newColor = colorOff
		colorName = "OFF"
	}

	padColors[pos] = newColor

	// Send SysEx update
	sysex := buildSysEx(padColors)
	if err := sendSysEx(sysex); err != nil {
		log.Printf("Error sending SysEx: %v", err)
		return
	}

	debugLog("Pad %d toggled -> %s", note, colorName)
}

// Set a pad's LED state directly (not toggle)
func setPad(note uint8, on bool) {
	stateMutex.Lock()
	defer stateMutex.Unlock()

	pos, ok := noteToPayloadPos[note]
	if !ok {
		return
	}

	// Skip if already in desired state
	if padState[note] == on {
		return
	}

	padState[note] = on

	// Determine color based on state and row
	var newColor Color
	var colorName string
	if on {
		if isTopRow[note] {
			newColor = colorTopRow // Blue
			colorName = "BLUE"
		} else {
			newColor = colorBottomRow // Amber
			colorName = "AMBER"
		}
	} else {
		newColor = colorOff
		colorName = "OFF"
	}

	padColors[pos] = newColor

	// Send SysEx update
	sysex := buildSysEx(padColors)
	if err := sendSysEx(sysex); err != nil {
		log.Printf("Error sending SysEx: %v", err)
		return
	}

	debugLog("Pad %d set -> %s", note, colorName)
}

// Handle amber (bottom row) press - toggles amber AND sets controlled blues to opposite
// All updates happen atomically in a single SysEx message
func handleAmberPress(amberNote uint8) {
	stateMutex.Lock()
	defer stateMutex.Unlock()

	amberPos := noteToPayloadPos[amberNote]
	blueNotes := amberToBlues[amberNote]

	// Toggle amber
	padState[amberNote] = !padState[amberNote]
	amberIsOn := padState[amberNote]

	// Update amber color
	if amberIsOn {
		padColors[amberPos] = colorBottomRow // Amber ON
	} else {
		padColors[amberPos] = colorOff // Amber OFF
	}

	// Set all controlled blues to OPPOSITE of amber
	var blueNames []uint8
	for _, blueNote := range blueNotes {
		bluePos := noteToPayloadPos[blueNote]
		padState[blueNote] = !amberIsOn
		if !amberIsOn {
			padColors[bluePos] = colorTopRow // Blue ON
		} else {
			padColors[bluePos] = colorOff // Blue OFF
		}
		blueNames = append(blueNames, blueNote)
	}

	if amberIsOn {
		debugLog("Amber %d ON, Blues %v OFF", amberNote, blueNames)
	} else {
		debugLog("Amber %d OFF, Blues %v ON", amberNote, blueNames)
	}

	// Send single SysEx with all updates
	sysex := buildSysEx(padColors)
	if err := sendSysEx(sysex); err != nil {
		log.Printf("Error sending SysEx: %v", err)
	}
}

// Handle blue (top row) press - toggles blue AND turns off any controlling ambers
func handleBluePress(blueNote uint8) {
	stateMutex.Lock()
	defer stateMutex.Unlock()

	bluePos := noteToPayloadPos[blueNote]

	// Toggle blue
	padState[blueNote] = !padState[blueNote]
	blueIsOn := padState[blueNote]

	// Update blue color
	if blueIsOn {
		padColors[bluePos] = colorTopRow // Blue ON
	} else {
		padColors[bluePos] = colorOff // Blue OFF
	}

	// If blue is turning ON, turn off any ambers that were controlling it
	var ambersOff []uint8
	if blueIsOn {
		for _, amberNote := range blueToAmbers[blueNote] {
			if padState[amberNote] { // Amber is currently ON
				padState[amberNote] = false
				amberPos := noteToPayloadPos[amberNote]
				padColors[amberPos] = colorOff
				ambersOff = append(ambersOff, amberNote)
			}
		}
	}

	if len(ambersOff) > 0 {
		debugLog("Blue %d ON, Ambers %v OFF", blueNote, ambersOff)
	} else if blueIsOn {
		debugLog("Blue %d ON", blueNote)
	} else {
		debugLog("Blue %d OFF", blueNote)
	}

	// Send single SysEx with all updates
	sysex := buildSysEx(padColors)
	if err := sendSysEx(sysex); err != nil {
		log.Printf("Error sending SysEx: %v", err)
	}
}

// Handle knob (CC) change - controls blue LED based on value
// value < 2: blue turns off
// value >= 2: blue turns on with brightness scaled from knob value
// Knob range 0-64 maps to LED brightness 0-127
func handleKnobChange(cc uint8, value uint8) {
	blueNote, ok := knobToBlue[cc]
	if !ok {
		return
	}

	stateMutex.Lock()
	defer stateMutex.Unlock()

	pos, ok := noteToPayloadPos[blueNote]
	if !ok {
		return
	}

	if value < 2 {
		// Turn off
		if !padState[blueNote] {
			return // Already off
		}
		padState[blueNote] = false
		padColors[pos] = colorOff
		debugLog("Knob CC%d=%d -> Blue %d OFF", cc, value, blueNote)
	} else {
		// Turn on with scaled brightness (0-64 -> 0-127)
		brightness := value * 2
		if brightness > 127 {
			brightness = 127
		}
		padState[blueNote] = true
		padColors[pos] = Color{0, 0, brightness} // Blue with variable brightness
		debugLog("Knob CC%d=%d -> Blue %d ON (brightness %d)", cc, value, blueNote, brightness)
	}

	// Send SysEx update
	sysex := buildSysEx(padColors)
	if err := sendSysEx(sysex); err != nil {
		log.Printf("Error sending SysEx: %v", err)
		return
	}
}

func listPorts() {
	fmt.Println("Available MIDI Input Ports:")
	for i, in := range midi.GetInPorts() {
		fmt.Printf("  [%d] %s\n", i, in)
	}
	fmt.Println("\nAvailable MIDI Output Ports:")
	for i, out := range midi.GetOutPorts() {
		fmt.Printf("  [%d] %s\n", i, out)
	}
}

func main() {
	var (
		listOnly   bool
		outputPort string
		spyPort    string
		configPath string
		genConfig  string
		testMode   bool
	)

	flag.BoolVar(&listOnly, "list", false, "List available MIDI ports and exit")
	flag.StringVar(&outputPort, "out", "", "MIDI output port name (sends to LPD8)")
	flag.StringVar(&spyPort, "spy", "", "MIDI input to mirror button presses from (e.g., PLX-CRSS12)")
	flag.StringVar(&configPath, "config", "", "Path to config file (JSON)")
	flag.StringVar(&genConfig, "genconfig", "", "Generate default config file at path and exit")
	flag.BoolVar(&testMode, "test", false, "Test LED colors and exit")
	flag.BoolVar(&debugMode, "debug", false, "Enable debug logging")
	flag.Parse()

	defer midi.CloseDriver()

	// Generate config file if requested
	if genConfig != "" {
		cfg := defaultConfig()
		if err := saveConfig(genConfig, cfg); err != nil {
			log.Fatalf("Failed to write config: %v", err)
		}
		fmt.Printf("Default config written to: %s\n", genConfig)
		return
	}

	// Load config (or use defaults)
	var cfg Config
	if configPath != "" {
		var err error
		cfg, err = loadConfig(configPath)
		if err != nil {
			log.Fatalf("Failed to load config: %v", err)
		}
		log.Printf("Loaded config from: %s", configPath)
	} else {
		cfg = defaultConfig()
	}
	buildMappings(cfg)

	if listOnly {
		listPorts()
		return
	}

	if outputPort == "" {
		fmt.Println("Usage: lpd8-led-bridge -out \"LPD8 Port Name\" [options]")
		fmt.Println()
		fmt.Println("Options:")
		fmt.Println("  -spy \"PORT\"      Mirror button presses from another device")
		fmt.Println("  -config FILE     Load config from JSON file")
		fmt.Println("  -genconfig FILE  Generate default config file and exit")
		fmt.Println("  -list            List available MIDI ports")
		fmt.Println("  -test            Test LED colors")
		fmt.Println()
		listPorts()
		os.Exit(1)
	}

	// Find output port
	outPort, err := midi.FindOutPort(outputPort)
	if err != nil {
		log.Fatalf("Output port not found: %s (%v)", outputPort, err)
	}

	// Create send function using the output port
	send, err := midi.SendTo(outPort)
	if err != nil {
		log.Fatalf("Failed to open output port: %v", err)
	}

	// Set the global send function for SysEx
	sendSysEx = func(data []byte) error {
		return send(data)
	}

	// Test mode - cycle through colors
	if testMode {
		log.Println("Test mode: cycling LED colors...")
		log.Println("Format: F0 47 7F 4C 06 00 30 [48 bytes] F7")

		testColors := []struct {
			name  string
			color Color
		}{
			{"RED", Color{127, 0, 0}},
			{"GREEN", Color{0, 127, 0}},
			{"BLUE", Color{0, 0, 127}},
			{"WHITE", Color{127, 127, 127}},
			{"OFF", Color{0, 0, 0}},
		}

		for _, tc := range testColors {
			var colors [8]Color
			for i := range colors {
				colors[i] = tc.color
			}

			sysex := buildSysEx(colors)
			fmt.Printf("\n%s - Sending %d bytes: % X\n", tc.name, len(sysex), sysex)

			if err := sendSysEx(sysex); err != nil {
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Println("Sent!")
			}

			fmt.Print("Press Enter for next color...")
			fmt.Scanln()
		}

		log.Println("Test complete")
		return
	}

	// Initialize pad states and LED colors from config
	// Top row: ON by default (Blue)
	// Bottom row: OFF by default (Black)
	for _, note := range cfg.LPD8.TopRow {
		n := uint8(note)
		padState[n] = true // Top row starts ON
		pos := noteToPayloadPos[n]
		padColors[pos] = colorTopRow // Blue
	}
	for _, note := range cfg.LPD8.BottomRow {
		n := uint8(note)
		padState[n] = false // Bottom row starts OFF
		pos := noteToPayloadPos[n]
		padColors[pos] = colorOff // Off
	}

	sysex := buildSysEx(padColors)
	sendSysEx(sysex)
	log.Println("Initial LED state set: Top=Blue(ON), Bottom=OFF")

	// Shared button press handler - processes a pad note press
	processPadPress := func(source string, note uint8) {
		// Check if this is a valid pad note
		if _, ok := noteToPayloadPos[note]; ok {
			debugLog("%s pad press: note=%d", source, note)

			// Bottom row (amber) - toggle amber AND set controlled blues to opposite
			if _, isAmber := amberToBlues[note]; isAmber {
				handleAmberPress(note)
			} else {
				// Top row (blue) - toggle and turn off controlling ambers
				handleBluePress(note)
			}
		}
	}

	// MIDI message handler for LPD8
	handler := func(msg midi.Message, timestampms int32) {
		var ch, key, val uint8

		switch {
		case msg.GetNoteOn(&ch, &key, &val):
			// Only respond to configured channel and actual pad presses (vel > 0)
			if ch == lpd8Channel && val > 0 {
				processPadPress("LPD8", key)
			}
		case msg.GetControlChange(&ch, &key, &val):
			// Handle knob (CC) changes - accept configured channel or all (255)
			if lpd8KnobChannel == 255 || ch == lpd8KnobChannel {
				handleKnobChange(key, val)
			}
		}
	}

	var stopFuncs []func()

	// Set up spy port listener if specified (PLX-CRSS12 button presses)
	if spyPort != "" {
		spyIn, err := midi.FindInPort(spyPort)
		if err != nil {
			log.Fatalf("Spy port not found: %s (%v)", spyPort, err)
		}

		// Spy handler - mirror button presses from PLX-CRSS12
		// Accept any channel since we don't know what channel the CRSS12 uses
		spyHandler := func(msg midi.Message, timestampms int32) {
			var ch, note, vel uint8

			switch {
			case msg.GetNoteOn(&ch, &note, &vel):
				if vel > 0 {
					// Remap CRSS12 notes if needed (32-35 -> 40-43)
					mappedNote := note
					if remapped, ok := crss12NoteRemap[note]; ok {
						mappedNote = remapped
						debugLog("Spy: ch=%d note=%d->%d vel=%d", ch, note, mappedNote, vel)
					} else {
						debugLog("Spy: ch=%d note=%d vel=%d", ch, note, vel)
					}
					processPadPress("CRSS12", mappedNote)
				}
			}
		}

		stop, err := midi.ListenTo(spyIn, spyHandler)
		if err != nil {
			log.Fatalf("Failed to listen to spy port: %v", err)
		}
		stopFuncs = append(stopFuncs, stop)
		log.Printf("Spy mode: mirroring button presses from %s", spyPort)
	}

	// Listen to all MIDI inputs for LPD8 pad presses
	inPorts := midi.GetInPorts()
	for _, inPort := range inPorts {
		// Skip the spy port to avoid double-handling
		if spyPort != "" && inPort.String() == spyPort {
			continue
		}
		stop, err := midi.ListenTo(inPort, handler)
		if err != nil {
			log.Printf("Warning: couldn't listen to %s: %v", inPort, err)
			continue
		}
		stopFuncs = append(stopFuncs, stop)
		log.Printf("Listening on: %s", inPort)
	}

	if len(stopFuncs) == 0 {
		log.Println("WARNING: No MIDI input ports found!")
	}

	log.Println("")
	log.Printf("LPD8 LED Bridge running")
	log.Printf("Sending to: %s", outputPort)
	if spyPort != "" {
		log.Printf("Mirroring: %s", spyPort)
	}
	log.Println("Press Ctrl+C to exit")

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	for _, stop := range stopFuncs {
		stop()
	}
	log.Println("Shutting down...")
}
