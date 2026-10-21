package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePSIAndCounterReset(t *testing.T) {
	v, e := ParsePSI("some avg10=2.00 avg60=0.50 avg300=0.10 total=123456\nfull avg10=0 total=42\n")
	if e != nil || v["some"] != 123456 || v["full"] != 42 {
		t.Fatalf("bad PSI: %v %v", v, e)
	}
	if _, ok := Delta(100, 4); ok {
		t.Fatal("reset became a delta")
	}
	if v, ok := Delta(3, 8); !ok || v != 5 {
		t.Fatal("valid delta failed")
	}
}
func TestMalformedPressureIsUnavailable(t *testing.T) {
	for _, input := range []string{"", "some total=broken", "full total=10"} {
		if _, e := ParsePSI(input); e == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
func TestProcFixturesAndDiskUnits(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	os.MkdirAll(filepath.Join(proc, "pressure"), 0700)
	os.MkdirAll(filepath.Join(proc, "sys/kernel/random"), 0700)
	write := func(p, value string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(proc, p), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("sys/kernel/random/boot_id", "boot-one")
	write("stat", "cpu 100 0 50 800 50 0 0 0\ncpu0 100 0 50 800 50 0 0 0\n")
	write("meminfo", "MemTotal: 1000 kB\nMemAvailable: 400 kB\nSwapTotal: 100 kB\nSwapFree: 100 kB\n")
	write("diskstats", "8 0 sda 10 0 100 20 10 0 100 20 0 30 40\n")
	write("pressure/io", "some total=100\nfull total=0\n")
	c := NewCollector(Config{Proc: proc, Sys: filepath.Join(root, "sys"), Sampling: 5})
	now := time.Now()
	f, _, caps, _ := c.Collect(now)
	for _, s := range f.Samples {
		if s.EntityID == "host" {
			if _, ok := s.Values["cpu.busy_pct"]; ok {
				t.Fatal("first counter sample is not a baseline")
			}
			if s.Values["memory.used_bytes"] != 600*1024 {
				t.Fatal("memory conversion")
			}
		}
	}
	if caps["psi.cpu"].Status != "unsupported" {
		t.Fatal("missing PSI must be unavailable")
	}
	write("stat", "cpu 200 0 100 1100 100 0 0 0\ncpu0 200 0 100 1100 100 0 0 0\n")
	write("diskstats", "8 0 sda 15 0 120 30 12 0 110 24 0 130 240\n")
	write("pressure/io", "some total=1000100\nfull total=0\n")
	f, _, _, _ = c.Collect(now.Add(5 * time.Second))
	for _, s := range f.Samples {
		if s.EntityID == "host" && s.Values["io.some_pct"] != 20 {
			t.Fatal("PSI denominator")
		}
		if s.EntityID == "device:8:0" && s.Values["disk.read_bytes"] != 20*512 {
			t.Fatal("disk sectors must be 512 bytes")
		}
	}
}
func TestBufferBoundsAndAckIdentity(t *testing.T) {
	b := &Buffer{}
	now := time.Now()
	b.Push([]byte("first"), now)
	b.Push([]byte("second"), now.Add(11*time.Minute))
	if string(b.peek()) != "second" || b.takeDropped() != 1 {
		t.Fatal("old batch not dropped")
	}
	b.ack([]byte("first"))
	if string(b.peek()) != "second" {
		t.Fatal("ack removed a newer batch")
	}
	b.ack([]byte("second"))
	if b.peek() != nil {
		t.Fatal("ack failed")
	}
	b.Push([]byte(strings.Repeat("x", 33<<20)), now)
	if b.peek() != nil || b.takeDropped() != 1 {
		t.Fatal("byte bound")
	}
}
func FuzzPressure(f *testing.F) {
	f.Add("some avg10=0 total=10\nfull total=2")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 10000 {
			return
		}
		_, _ = ParsePSI(input)
	})
}
