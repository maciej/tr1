package tokfm

import (
	"strings"
	"testing"
)

func TestExtractScheduleIslandURL(t *testing.T) {
	page := `<html><head><link rel="preload" as="fetch" href="/_server-islands/ScheduleListIsland?e=A%2B&p=&amp;s=" crossorigin="anonymous"></head></html>`

	got, err := ExtractScheduleIslandURL("https://audycje.tokfm.pl/ramowka", page)
	if err != nil {
		t.Fatalf("ExtractScheduleIslandURL returned error: %v", err)
	}
	want := "https://audycje.tokfm.pl/_server-islands/ScheduleListIsland?e=A%2B&p=&s="
	if got != want {
		t.Fatalf("schedule island URL = %q, want %q", got, want)
	}
}

func TestParseSchedule(t *testing.T) {
	fragment := `<div class="days">
<ul class="tok-js__play_list day flex-col gap-2 scheldule-list hidden" data-day="3">
<li>
  <div> 07:00 </div>
  <img src="/img/poranek.png" alt="Ramówka Poranek TOK FM">
  <h3 class="tok-schedule__program--name font-bold text-base text-black"><a href="/audycja/117,Poranek-TOK-FM"> Poranek TOK FM </a></h3>
  <h3 class="tok-schedule__program--name font-bold text-base text-primary"><a href="/podcast/192671,Czy-Ameryka"> Czy Ameryka zwija relacje z Polską? </a></h3>
  <div><a href="/prowadzacy/3,Dominika-Wielowieyska"> Dominika Wielowieyska </a><a href="/prowadzacy/3,Dominika-Wielowieyska"> Dominika Wielowieyska </a></div>
  <button class="tok-podcasts__button--play" data-id="192671"><span> 13 min </span></button>
</li>
</ul>
<ul class="tok-js__play_list day flex-col gap-2 scheldule-list hidden" data-day="6">
<li>
  <div> 10:00 </div>
  <h3 class="tok-schedule__program--name font-bold text-base text-black"><a href="/audycja/685,Wiesz-co-jesz"> Wiesz, co jesz </a></h3>
  <div><a href="/prowadzacy/44,Patrycja-Wanat"> Patrycja Wanat </a></div>
</li>
</ul>
</div>`

	entries, err := ParseSchedule(fragment, "https://audycje.tokfm.pl/_server-islands/ScheduleListIsland?x=1")
	if err != nil {
		t.Fatalf("ParseSchedule returned error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries length = %d, want 2", len(entries))
	}
	first := entries[0]
	if first.Day != "Thursday" || first.Time != "07:00" || first.Programme != "Poranek TOK FM" {
		t.Fatalf("first entry basics = %+v", first)
	}
	if first.Episode != "Czy Ameryka zwija relacje z Polską?" {
		t.Fatalf("episode = %q", first.Episode)
	}
	if first.ProgrammeURL != "https://audycje.tokfm.pl/audycja/117,Poranek-TOK-FM" {
		t.Fatalf("programme URL = %q", first.ProgrammeURL)
	}
	if first.ImageURL != "https://audycje.tokfm.pl/img/poranek.png" {
		t.Fatalf("image URL = %q", first.ImageURL)
	}
	if first.PodcastID != "192671" || first.Duration != "13 min" {
		t.Fatalf("podcast fields = id %q duration %q", first.PodcastID, first.Duration)
	}
	if got := strings.Join(first.Hosts, ", "); got != "Dominika Wielowieyska" {
		t.Fatalf("hosts = %q", got)
	}
	second := entries[1]
	if second.Day != "Sunday" || second.Episode != "" || strings.Join(second.Hosts, ", ") != "Patrycja Wanat" {
		t.Fatalf("second entry = %+v", second)
	}
}
