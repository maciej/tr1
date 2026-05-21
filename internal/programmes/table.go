package programmes

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

func WriteTable(w io.Writer, schedule Schedule) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "DAY\tTIME\tPROGRAMME\tEPISODE\tHOSTS\tDURATION"); err != nil {
		return err
	}
	for _, entry := range schedule.Entries {
		episode := entry.Episode
		if episode == "" {
			episode = "-"
		}
		hosts := strings.Join(entry.Hosts, ", ")
		if hosts == "" {
			hosts = "-"
		}
		duration := entry.Duration
		if duration == "" {
			duration = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", entry.Day, entry.Time, entry.Programme, episode, hosts, duration); err != nil {
			return err
		}
	}
	return tw.Flush()
}
