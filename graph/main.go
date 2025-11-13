package main

import (
	"fmt"
	"os"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/wcharczuk/go-chart/v2"
)

func main() {
	times := make([]time.Time, 50)
	for i := range times {
		times[i] = time.Now().AddDate(0, 0, i)
	}
	seq := chart.Seq{Sequence: chart.NewRandomSequence().WithMin(1024.0).WithMax(1_048_576.0).WithLen(50)}
	sizes := seq.Values()

	sizeSeries := chart.TimeSeries{
		Name:    "Binary Sizes",
		XValues: times,
		YValues: sizes,
	}

	graph := chart.Chart{
		XAxis: chart.XAxis{
			Name:           "Commit Time",
			ValueFormatter: chart.TimeDateValueFormatter,
			TickPosition:   chart.TickPositionBetweenTicks,
		},
		YAxis: chart.YAxis{
			Name: "Binary Sizes",
			ValueFormatter: func(v any) string {
				mb := uint64(v.(float64))

				return fmt.Sprintf("%s (%d B)", humanize.IBytes(mb), mb)
			},
		},
		Series: []chart.Series{
			sizeSeries,
			// smaSeries,
		},
	}

	graphFile, err := os.Create("graph.png")
	if err != nil {
		panic(fmt.Errorf("creating graph file: %w", err))
	}
	defer graphFile.Close()
	err = graph.Render(chart.PNG, graphFile)
	if err != nil {
		panic(fmt.Errorf("rendering graph: %w", err))
	}
}
