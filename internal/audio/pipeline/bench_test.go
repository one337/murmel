package pipeline

import (
	"context"
	"io"
	"math/rand"
	"testing"
	"time"
)

// Budgets (spec): capture→encode-ready ≤ 2 ms per 480-sample frame,
// encode ≤ 3 ms. Benchmarks are the acceptance evidence; the latency
// guard below fails the benchmark run if a budget is blown.
const captureToEncodeBudgetNS = uint64(2 * time.Millisecond)

func BenchmarkRMS(b *testing.B) {
	frame := noiseFrameAt(rand.New(rand.NewSource(1)), 500)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RMS(frame)
	}
}

func BenchmarkVADSample(b *testing.B) {
	v := NewVAD(VADConfig{})
	frame := noiseFrameAt(rand.New(rand.NewSource(2)), 300)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Sample(frame)
	}
}

// BenchmarkPipelineLatency drives the real pipeline (staged goroutines,
// pools, queues, taps, noop encode) at a device-like pace and reports
// the per-frame capture→encode-ready latency from the pipeline's own
// instrumentation. ns/op ≈ per-frame pipeline throughput cost.
func BenchmarkPipelineLatency(b *testing.B) {
	var pkt [2]byte
	p := New(Config{
		Source:    paced(100, b.N),
		Taps:      []Tap{TapFunc(func(*Frame) {})},
		Encode:    func(pcm []int16) ([]byte, error) { return pkt[:], nil },
		PacketOut: nopPacketSink{},
		PacketIn:  eofPacketSource{},
		Decode:    func([]byte) ([]int16, error) { return nil, io.EOF },
		Sink:      sinkFunc(func([]int16) error { return nil }),
	})
	p.Start(context.Background())
	p.Wait()

	st := p.Snapshot()
	if st.Encoded != uint64(b.N) {
		b.Fatalf("encoded %d of %d frames", st.Encoded, b.N)
	}
	avg := st.CaptureToEncodeReadyNS / st.Encoded
	b.ReportMetric(float64(avg), "ns/c2e-avg")
	b.ReportMetric(float64(st.MaxCaptureToEncodeNS), "ns/c2e-max")
	if avg > captureToEncodeBudgetNS {
		b.Fatalf("capture→encode-ready avg %v exceeds 2 ms budget", time.Duration(avg))
	}
	b.ReportAllocs()
}

type eofPacketSource struct{}

func (eofPacketSource) ReadPacket() ([]byte, error) { return nil, io.EOF }
