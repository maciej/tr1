package tr1

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"
	"unsafe"
)

func writeStations(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ALIAS\tNAME\tLANGUAGE\tURL\tALIASES"); err != nil {
		return err
	}
	for _, s := range stations {
		primaryAlias := ""
		if len(s.Aliases) > 0 {
			primaryAlias = s.Aliases[0]
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", primaryAlias, s.Name, s.Language, s.URL, strings.Join(s.Aliases, ", ")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeBenchmarkResults(w io.Writer, results []benchResult) error {
	if _, err := fmt.Fprintln(w, "BACKEND\tMODEL\tWER\tWORDS\tTIME\tRTF\tTEXT"); err != nil {
		return err
	}
	for _, r := range results {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%.3f\t%d\t%s\t%.3f\t%s\n", r.Backend, r.Model, r.WER, r.Words, r.Duration.Round(time.Millisecond), r.RTF, oneLine(r.Text)); err != nil {
			return err
		}
	}
	return nil
}

func printStableWords(ctx context.Context, cfg Config, writer *terminalWordWriter, resp whisperResponse, stableUntil float64, printedUntil *float64) error {
	printed := 0
	for _, word := range resp.Words {
		start := resp.Offset + word.Start
		end := resp.Offset + word.End
		if end <= *printedUntil+0.05 || start < *printedUntil-0.25 || end > stableUntil {
			continue
		}
		text := strings.TrimSpace(word.Word)
		if text == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := writer.writeWord(text, needsSpace(text)); err != nil {
			return err
		}
		*printedUntil = end
		printed++
		if cfg.WordDelay > 0 {
			time.Sleep(cfg.WordDelay)
		}
	}
	if printed > 0 {
		status(cfg, "whisper", fmt.Sprintf("window %d: printed %d words", resp.Seq, printed))
	}
	return nil
}

type terminalWordWriter struct {
	out            io.Writer
	columns        *terminalColumns
	col            int
	spinnerEnabled bool
	spinnerVisible bool
	spinnerFrame   int
	spinnerWidth   int
	tuningVisible  bool
	tuningFrame    int
	tuningWidth    int
	stopResize     func()
}

type terminalColumns struct {
	fd       uintptr
	width    atomic.Int32
	enabled  bool
	provider func(uintptr) (int, bool)
}

var terminalSpinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
var terminalTuningFrames = []string{"📻 Tuning in.", "📻 Tuning in..", "📻 Tuning in..."}

func newTerminalWordWriter(file *os.File, spinner bool) *terminalWordWriter {
	columns := newTerminalColumns(file)
	stopResize := startTerminalResizeListener(context.Background(), columns, nil)
	return &terminalWordWriter{
		out:            file,
		columns:        columns,
		spinnerEnabled: spinner && columns.enabled,
		stopResize:     stopResize,
	}
}

func newTerminalColumns(file *os.File) *terminalColumns {
	columns := &terminalColumns{
		fd:       file.Fd(),
		provider: terminalWidth,
	}
	if info, err := file.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
		columns.enabled = true
		columns.refresh()
	}
	return columns
}

func newTestTerminalWordWriter(out io.Writer, width int) *terminalWordWriter {
	columns := &terminalColumns{enabled: width > 0}
	columns.width.Store(int32(width))
	return &terminalWordWriter{out: out, columns: columns}
}

func (w *terminalWordWriter) close() {
	_ = w.clearEphemeral()
	if w.stopResize != nil {
		w.stopResize()
	}
}

func (w *terminalWordWriter) writeWord(text string, trailingSpace bool) error {
	if err := w.clearEphemeral(); err != nil {
		return err
	}
	width := w.columns.current()
	textWidth := displayWidth(text)
	spaceWidth := 0
	if trailingSpace {
		spaceWidth = 1
	}
	if width > 0 && w.col > 0 && w.col+textWidth+spaceWidth > width {
		if _, err := fmt.Fprint(w.out, "\n"); err != nil {
			return err
		}
		w.col = 0
	}
	if _, err := fmt.Fprint(w.out, text); err != nil {
		return err
	}
	w.col += textWidth
	if trailingSpace {
		if _, err := fmt.Fprint(w.out, " "); err != nil {
			return err
		}
		w.col += spaceWidth
	}
	return nil
}

func (w *terminalWordWriter) tickSpinner() error {
	if w == nil || !w.spinnerEnabled || len(terminalSpinnerFrames) == 0 {
		return nil
	}
	if err := w.clearEphemeral(); err != nil {
		return err
	}
	text := " " + terminalSpinnerFrames[w.spinnerFrame%len(terminalSpinnerFrames)]
	textWidth := displayWidth(text)
	if width := w.columns.current(); width > 0 && w.col+textWidth > width {
		return nil
	}
	if _, err := fmt.Fprint(w.out, text); err != nil {
		return err
	}
	w.spinnerVisible = true
	w.spinnerWidth = textWidth
	w.spinnerFrame++
	return nil
}

func (w *terminalWordWriter) tickTuning() error {
	if w == nil || !w.spinnerEnabled || len(terminalTuningFrames) == 0 {
		return nil
	}
	if err := w.clearEphemeral(); err != nil {
		return err
	}
	text := terminalTuningFrames[w.tuningFrame%len(terminalTuningFrames)]
	textWidth := displayWidth(text)
	if width := w.columns.current(); width > 0 && w.col+textWidth > width {
		return nil
	}
	if _, err := fmt.Fprint(w.out, text); err != nil {
		return err
	}
	w.tuningVisible = true
	w.tuningWidth = textWidth
	w.tuningFrame++
	return nil
}

func (w *terminalWordWriter) clearEphemeral() error {
	if err := w.clearSpinner(); err != nil {
		return err
	}
	if err := w.clearTuning(); err != nil {
		return err
	}
	return nil
}

func (w *terminalWordWriter) clearSpinner() error {
	if w == nil || !w.spinnerVisible {
		return nil
	}
	if err := w.eraseEphemeral(w.spinnerWidth); err != nil {
		return err
	}
	w.spinnerVisible = false
	w.spinnerWidth = 0
	return nil
}

func (w *terminalWordWriter) clearTuning() error {
	if w == nil || !w.tuningVisible {
		return nil
	}
	if err := w.eraseEphemeral(w.tuningWidth); err != nil {
		return err
	}
	w.tuningVisible = false
	w.tuningWidth = 0
	return nil
}

func (w *terminalWordWriter) eraseEphemeral(width int) error {
	if width <= 0 {
		return nil
	}
	erase := strings.Repeat("\b", width) + strings.Repeat(" ", width) + strings.Repeat("\b", width)
	_, err := fmt.Fprint(w.out, erase)
	return err
}

func (c *terminalColumns) current() int {
	if c == nil || !c.enabled {
		return 0
	}
	return int(c.width.Load())
}

func (c *terminalColumns) refresh() {
	if c == nil || !c.enabled || c.provider == nil {
		return
	}
	if width, ok := c.provider(c.fd); ok && width > 0 {
		c.width.Store(int32(width))
	}
}

func startTerminalResizeListener(ctx context.Context, columns *terminalColumns, signals chan os.Signal) func() {
	if columns == nil || !columns.enabled {
		return nil
	}
	if signals == nil {
		signals = make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGWINCH)
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-signals:
				columns.refresh()
			}
		}
	}()
	return func() {
		cancel()
		signal.Stop(signals)
	}
}

type winsize struct {
	row    uint16
	col    uint16
	xpixel uint16
	ypixel uint16
}

func terminalWidth(fd uintptr) (int, bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if errno != 0 || ws.col == 0 {
		return 0, false
	}
	return int(ws.col), true
}

func displayWidth(text string) int {
	width := 0
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r':
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		case isWideRune(r):
			width += 2
		default:
			width++
		}
	}
	return width
}

func isWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) ||
		(r >= 0x2329 && r <= 0x232A) ||
		(r >= 0x2E80 && r <= 0xA4CF) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE19) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6) ||
		(r >= 0x1F300 && r <= 0x1FAFF)
}

func appendStableWords(resp whisperResponse, stableUntil float64, printedUntil *float64, out *[]string) {
	for _, word := range resp.Words {
		start := resp.Offset + word.Start
		end := resp.Offset + word.End
		if end <= *printedUntil+0.05 || start < *printedUntil-0.25 || end > stableUntil {
			continue
		}
		text := strings.TrimSpace(word.Word)
		if text == "" {
			continue
		}
		*out = append(*out, text)
		*printedUntil = end
	}
}

func needsSpace(s string) bool {
	if s == "" {
		return false
	}
	last := []rune(s)[len([]rune(s))-1]
	return !strings.ContainsRune("([{/", last)
}

func oneLine(s string) string {
	re := regexp.MustCompile(`\s+`)
	s = re.ReplaceAllString(strings.TrimSpace(s), " ")
	if len([]rune(s)) > 180 {
		return string([]rune(s)[:180]) + "..."
	}
	return s
}

type prefixWriter struct {
	prefix string
	buf    []byte
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		if line != "" {
			fmt.Fprintf(os.Stderr, "\n[%s] %s\n", w.prefix, line)
		}
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func status(cfg Config, scope, msg string) {
	if !cfg.Verbose {
		return
	}
	fmt.Fprintf(os.Stderr, "\n[%s] %s\n", scope, msg)
}

func prefixedStderr(cfg Config, prefix string) io.Writer {
	if !cfg.Verbose {
		return io.Discard
	}
	return &prefixWriter{prefix: prefix}
}

func Fatal(program string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", program, err)
	os.Exit(1)
}
