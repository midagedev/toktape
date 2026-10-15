package tape

import (
	"math"
	"testing"
)

func TestVectorRoundTripAndMetrics(t *testing.T) {
	v := []float32{0.5, -1.25, 3, 0}
	got, err := DecodeVector(EncodeVector(v))
	if err != nil || len(got) != len(v) {
		t.Fatalf("decode: %v %v", got, err)
	}
	for i := range v {
		if got[i] != v[i] {
			t.Fatalf("value %d = %v, want %v", i, got[i], v[i])
		}
	}
	if _, err := DecodeVector("AAA="); err == nil {
		t.Error("two bytes decoded as a vector")
	}
	if c := Cosine(v, v); math.Abs(c-1) > 1e-12 {
		t.Errorf("self cosine %v", c)
	}
	if c := Cosine([]float32{1, 0}, []float32{0, 1}); c != 0 {
		t.Errorf("orthogonal cosine %v", c)
	}
	if c := Cosine([]float32{0, 0}, []float32{1, 1}); !math.IsNaN(c) {
		t.Errorf("zero vector cosine %v, want NaN", c)
	}
	if d := MaxAbsDiff([]float32{1, 2, 3}, []float32{1, 2.5, 2}); d != 1 {
		t.Errorf("max diff %v", d)
	}
	if n := L2Norm([]float32{3, 4}); n != 5 {
		t.Errorf("norm %v", n)
	}
}
