package orchestration

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestSplitAndAssembleHaLowImage(t *testing.T) {
	data := append([]byte{0xff, 0xd8}, bytes.Repeat([]byte("h"), 17000)...)
	data = append(data, 0xff, 0xd9)
	image := Image{
		RequestID:        "capture-001",
		CameraID:         "halow-camera",
		CapturedAt:       testNow,
		TimeSynchronized: true,
		Pose:             testPose(testNow),
		MediaType:        "image/jpeg",
		Data:             data,
	}

	manifest, chunks, err := SplitHaLowImage(image, DefaultHaLowChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != HaLowProtocolVersion || manifest.ChunkCount != 3 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if len(manifest.SHA256) != 64 {
		t.Fatalf("unexpected SHA-256 %q", manifest.SHA256)
	}

	reordered := []HaLowImageChunk{chunks[2], chunks[0], chunks[1]}
	assembled, err := AssembleHaLowImage(manifest, reordered, DefaultMaxImageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(assembled.Data, image.Data) {
		t.Fatal("assembled HaLow image differs from input")
	}
	if assembled.RequestID != image.RequestID ||
		assembled.CameraID != image.CameraID ||
		!assembled.TimeSynchronized {
		t.Fatalf("assembled metadata differs: %+v", assembled)
	}
	if !reflect.DeepEqual(assembled.Pose, image.Pose) {
		t.Fatalf("assembled camera pose differs:\n got %+v\nwant %+v", assembled.Pose, image.Pose)
	}

	ack, err := NewHaLowImageAck(manifest, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if !ack.Persisted || ack.RequestID != image.RequestID || ack.SHA256 != manifest.SHA256 {
		t.Fatalf("unexpected acknowledgement: %+v", ack)
	}
	if err := ValidateHaLowImageAck(manifest, ack); err != nil {
		t.Fatal(err)
	}
}

func TestAssembleHaLowImageRejectsCorruptedChunk(t *testing.T) {
	image := testImage("capture-001", "halow-camera", testNow, "halow")
	manifest, chunks, err := SplitHaLowImage(image, 4)
	if err != nil {
		t.Fatal(err)
	}
	chunks[0].Data[2] ^= 0xff

	_, err = AssembleHaLowImage(manifest, chunks, DefaultMaxImageBytes)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSplitHaLowImageRejectsIncompleteJPEG(t *testing.T) {
	image := testImage("capture-001", "halow-camera", testNow, "halow")
	image.Data = image.Data[:len(image.Data)-1]

	_, _, err := SplitHaLowImage(image, DefaultHaLowChunkBytes)
	if err == nil || !strings.Contains(err.Error(), "JPEG payload is incomplete") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRejectedHaLowAckRequiresReason(t *testing.T) {
	image := testImage("capture-001", "halow-camera", testNow, "halow")
	manifest, _, err := SplitHaLowImage(image, DefaultHaLowChunkBytes)
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewHaLowImageAck(manifest, false, "")
	if err == nil {
		t.Fatal("rejected acknowledgement without a reason was accepted")
	}
}

func TestValidateHaLowImageAckRejectsAnotherTransfer(t *testing.T) {
	image := testImage("capture-001", "halow-camera", testNow, "halow")
	manifest, _, err := SplitHaLowImage(image, DefaultHaLowChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := NewHaLowImageAck(manifest, true, "")
	if err != nil {
		t.Fatal(err)
	}
	ack.RequestID = "capture-002"

	if err := ValidateHaLowImageAck(manifest, ack); err == nil {
		t.Fatal("acknowledgement for another transfer was accepted")
	}
}
