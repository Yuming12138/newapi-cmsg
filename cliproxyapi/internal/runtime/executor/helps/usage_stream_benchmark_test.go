package helps

import "testing"

var (
	benchmarkOpenAIContentChunk = []byte(`data: {"choices":[{"delta":{"content":"hello"}}]}`)
	benchmarkOpenAIUsageChunk   = []byte(`data: {"usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`)
)

func BenchmarkStreamUsageBufferObserveOpenAIStreamContentChunk(b *testing.B) {
	var buffer StreamUsageBuffer
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		buffer.ObserveOpenAIStream(benchmarkOpenAIContentChunk)
	}
}

func BenchmarkStreamUsageBufferObserveOpenAIStream100Chunks(b *testing.B) {
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		var buffer StreamUsageBuffer
		for chunk := 0; chunk < 99; chunk++ {
			buffer.ObserveOpenAIStream(benchmarkOpenAIContentChunk)
		}
		buffer.ObserveOpenAIStream(benchmarkOpenAIUsageChunk)
	}
}
