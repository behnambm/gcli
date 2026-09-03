package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/behnambm/gcli/internal/api"
	"github.com/behnambm/gcli/internal/frames"
	"github.com/behnambm/gcli/internal/timeparse"
)

func init() {
	annotationsCmd.Flags().StringVar(&flagAnnTags, "tags", "", "comma-separated tags")
	annotationsCmd.Flags().StringVar(&flagAnnDashboard, "dashboard", "", "filter by dashboard uid")
	annotationsCmd.Flags().StringVar(&flagAnnFrom, "from", "now-24h", "start time")
	annotationsCmd.Flags().StringVar(&flagAnnTo, "to", "now", "end time")
	rootCmd.AddCommand(annotationsCmd)
}

var annotationsCmd = &cobra.Command{
	Use:   "annotations",
	Short: "Read annotations (incl. alert state changes)",
	Example: `  gcli annotations --tags deploy --from now-7d
  gcli annotations --dashboard GKmhl2-Mk`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		now := time.Now()
		fromMS, err := timeparse.ParseToEpochMS(flagAnnFrom, now)
		if err != nil {
			return fmt.Errorf("--from: %w", err)
		}
		toMS, err := timeparse.ParseToEpochMS(flagAnnTo, now)
		if err != nil {
			return fmt.Errorf("--to: %w", err)
		}
		var tags []string
		if flagAnnTags != "" {
			for _, t := range strings.Split(flagAnnTags, ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
				}
			}
		}
		return run(cmd, func(ctx context.Context, c *api.Client) (result, error) {
			anns, err := c.Annotations(ctx, fromMS, toMS, tags, flagAnnDashboard)
			if err != nil {
				return result{}, err
			}
			return result{res: frames.Result{
				Meta: frames.Meta{Datasource: "grafana", From: flagAnnFrom, To: flagAnnTo},
				Frames: []frames.Frame{{RefID: "A", Columns: []frames.Column{
					{Name: "Time", Values: annTimes(anns)},
					{Name: "Source", Values: annSources(anns)},
					{Name: "State", Values: annStates(anns)},
					{Name: "Text", Values: annTexts(anns)},
					{Name: "Tags", Values: annTagsOf(anns)},
				}}},
			}}, nil
		})
	},
}

func annTimes(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		out[i] = time.UnixMilli(a.TimeMS).UTC()
	}
	return out
}
func annSources(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		out[i] = a.Source
	}
	return out
}
func annStates(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		if a.AlertID != 0 && (a.NewState != "" || a.PrevState != "") {
			out[i] = a.PrevState + "→" + a.NewState
		} else {
			out[i] = ""
		}
	}
	return out
}
func annTexts(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		out[i] = a.Text
	}
	return out
}
func annTagsOf(anns []api.Annotation) []any {
	out := make([]any, len(anns))
	for i, a := range anns {
		out[i] = strings.Join(a.Tags, ",")
	}
	return out
}
