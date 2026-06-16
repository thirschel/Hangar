package proto

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	req := &Request{ID: 42, Method: MethodCreateSession, Session: "s1", Program: "copilot", Cols: 80, Rows: 24, AutoYes: true}
	if err := WriteFrame(&buf, req); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := ReadRequest(&buf)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.ID != 42 || got.Method != MethodCreateSession || got.Session != "s1" ||
		got.Program != "copilot" || got.Cols != 80 || got.Rows != 24 || !got.AutoYes {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestResponseRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	resp := &Response{ID: 7, OK: true, HostVersion: Version, Content: "hello\nworld",
		Sessions: []SessionInfo{{Name: "a", Alive: true, Program: "claude"}}}
	if err := WriteFrame(&buf, resp); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	got, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	if got.ID != 7 || !got.OK || got.HostVersion != Version || got.Content != "hello\nworld" ||
		len(got.Sessions) != 1 || got.Sessions[0].Name != "a" || !got.Sessions[0].Alive {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestMultipleFramesSequential(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 5; i++ {
		if err := WriteFrame(&buf, &Request{ID: uint64(i), Method: MethodHasUpdated, Session: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		got, err := ReadRequest(&buf)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if got.ID != uint64(i) {
			t.Fatalf("frame %d: got id %d", i, got.ID)
		}
	}
}

func TestReadFrameRejectsOversizeHeader(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrameSize+1)
	_, err := ReadFrameBytes(bytes.NewReader(hdr[:]))
	if err == nil {
		t.Fatal("expected oversize frame to be rejected")
	}
}

func TestWriteFrameRejectsOversizePayload(t *testing.T) {
	big := &Request{Method: "x", Data: make([]byte, MaxFrameSize)}
	if err := WriteFrame(io.Discard, big); err == nil {
		t.Fatal("expected oversize payload to be rejected")
	}
}

func TestReadFrameTruncatedBodyErrors(t *testing.T) {
	// header says 100 bytes, but only 3 follow -> ReadFull must error
	var b bytes.Buffer
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 100)
	b.Write(hdr[:])
	b.WriteString("abc")
	if _, err := ReadFrameBytes(&b); err == nil {
		t.Fatal("expected truncated body to error")
	}
}

func TestErrorfBuildsFailedResponse(t *testing.T) {
	r := Errorf(9, "bad %s", "thing")
	if r.ID != 9 || r.OK || r.Error != "bad thing" {
		t.Fatalf("unexpected: %+v", r)
	}
}
