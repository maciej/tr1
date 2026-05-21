package programmes

const CacheFile = "programmes.sqlite"

type Schedule struct {
	Station   string  `json:"station"`
	SourceURL string  `json:"source_url"`
	FetchedAt string  `json:"fetched_at"`
	Entries   []Entry `json:"entries"`
}

type Entry struct {
	DayIndex     int      `json:"day_index"`
	Day          string   `json:"day"`
	Time         string   `json:"time"`
	Programme    string   `json:"programme"`
	ProgrammeURL string   `json:"programme_url,omitempty"`
	Episode      string   `json:"episode,omitempty"`
	EpisodeURL   string   `json:"episode_url,omitempty"`
	Hosts        []string `json:"hosts,omitempty"`
	ImageURL     string   `json:"image_url,omitempty"`
	PodcastID    string   `json:"podcast_id,omitempty"`
	Duration     string   `json:"duration,omitempty"`
}

type CacheResult struct {
	Path       string
	NewEntries int64
}
