package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/components"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/html"
)

func main() {
	bar := charts.NewBar()
	bar.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{Title: "Binary Size"}),
		charts.WithToolboxOpts(opts.Toolbox{
			Right: "10%",
			Feature: &opts.ToolBoxFeature{
				SaveAsImage: &opts.ToolBoxFeatureSaveAsImage{},
			},
		}),
		charts.WithDataZoomOpts(opts.DataZoom{
			Type: "inside",
		}),
		charts.WithXAxisOpts(opts.XAxis{
			Name: "Date",
		}),
		charts.WithYAxisOpts(opts.YAxis{
			Name: "Size",
		}),
	)

	times := make([]string, 0)
	latest := time.Now().Unix()
	for range 50 {
		latest += rand.Int63n(604800)
		times = append(times, time.Unix(latest, 0).Format(time.RFC822))
	}

	sizes := make([]string, 0)
	items := make([]opts.BarData, 0)
	for range 50 {
		latest += rand.Int63n(604800)
		mb := rand.Intn(1_048_576-1024) + 1024
		size := humanize.IBytes(uint64(mb))
		sizes = append(sizes, size)
		items = append(items, opts.BarData{
			Value: mb,
			Name:  fmt.Sprintf("936e02: %s", size),
			ItemStyle: &opts.ItemStyle{
				Color: "green",
			},
		})
	}

	bar.SetXAxis(times)
	bar.AddSeries("master", items)

	page := components.NewPage()
	page.AddCharts(bar)

	var buf bytes.Buffer
	err := page.Render(&buf)
	if err != nil {
		panic(err)
	}

	f, err := os.Create("graph.html")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	m := minify.New()
	m.AddFunc("text/html", html.Minify)
	err = m.Minify("text/html", f, &buf)
	if err != nil {
		panic(err)
	}
}
