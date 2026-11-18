// Command sprite-anim converts an animated GIF into fully composited RGBA PNG
// frames plus a manifest.json, for the GNOME Shell sprite-animation spike.
//
//	go run . -in out/25.gif -out out/25
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/gif"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type manifestFrame struct {
	File    string `json:"file"`
	DelayMs int    `json:"delayMs"`
}

type manifest struct {
	Width    int             `json:"width"`
	Height   int             `json:"height"`
	Animated bool            `json:"animated"`
	Frames   []manifestFrame `json:"frames"`
}

func main() {
	in := flag.String("in", "", "input GIF path")
	out := flag.String("out", "", "output directory for frames and manifest.json")
	flag.Parse()
	if *in == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*in, *out); err != nil {
		log.Fatal(err)
	}
}

func run(in, out string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()
	g, err := gif.DecodeAll(f)
	if err != nil {
		return fmt.Errorf("decode %s: %w", in, err)
	}

	frames := Composite(g)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	m := manifest{
		Width:    g.Config.Width,
		Height:   g.Config.Height,
		Animated: len(frames) > 1,
		Frames:   make([]manifestFrame, 0, len(frames)),
	}
	delays := make([]string, 0, len(frames))
	total := 0
	for i, fr := range frames {
		name := fmt.Sprintf("frame-%03d.png", i)
		if err := writePNG(filepath.Join(out, name), fr); err != nil {
			return err
		}
		m.Frames = append(m.Frames, manifestFrame{File: name, DelayMs: fr.DelayMs})
		delays = append(delays, fmt.Sprint(fr.DelayMs))
		total += fr.DelayMs
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s: canvas %dx%d, %d frames, animated=%t, loop %d ms, delays ms [%s]\n",
		filepath.Base(in), m.Width, m.Height, len(frames), m.Animated, total, strings.Join(delays, " "))
	return nil
}

func writePNG(path string, fr Frame) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, fr.Image); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
