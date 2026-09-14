// Package favorites persists the user's favorite tracks to a TOML file in the
// cliamp config directory. Favorites are explicitly toggled by the user and
// span all playlists — a track favorited in playlist A appears when browsing
// the virtual "Favorites" playlist regardless of where it was starred.
//
// The store is safe for concurrent callers and writes atomically (temp file +
// rename) so a crash mid-write cannot leave a half-finished favorites.toml.
package favorites

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/internal/tomlutil"
	"github.com/bjarneo/cliamp/playlist"
)

// PlaylistName is the virtual playlist name surfaced to the UI by the local
// provider. Browsing this name returns favorite tracks newest-first.
const PlaylistName = "Favorites"

// Entry pairs a track with the wall-clock time it was favorited.
type Entry struct {
	Track       playlist.Track
	FavoritedAt time.Time
}

// Store reads and writes the favorites TOML file.
type Store struct {
	path string

	mu sync.Mutex
}

// New returns a Store backed by ~/.config/cliamp/favorites.toml. Returns nil if
// the config directory cannot be resolved.
func New() *Store {
	dir, err := appdir.Dir()
	if err != nil {
		return nil
	}
	return &Store{path: filepath.Join(dir, "favorites.toml")}
}

// NewAt returns a Store rooted at an explicit file path. Used by tests.
func NewAt(path string) *Store {
	return &Store{path: path}
}

// Path returns the on-disk file path.
func (s *Store) Path() string { return s.path }

// Toggle favorites a track. If the track is already favorited, it is removed
// (unfavorited). Returns true when the track is now favorited after the call.
// Empty paths are ignored and return false.
func (s *Store) Toggle(track playlist.Track) (bool, error) {
	if s == nil || strings.TrimSpace(track.Path) == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockFile()
	if err != nil {
		return false, err
	}
	defer func() { _ = unlock() }()

	entries, err := s.loadLocked()
	if err != nil {
		return false, fmt.Errorf("load favorites: %w", err)
	}

	idx := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Track.Path == track.Path
	})

	if idx >= 0 {
		// Already favorited — remove it.
		entries = slices.Delete(entries, idx, idx+1)
		return false, s.saveLocked(entries)
	}

	// Not yet favorited — add it at the front (newest first).
	entry := Entry{Track: track, FavoritedAt: time.Now()}
	entries = append([]Entry{entry}, entries...)
	return true, s.saveLocked(entries)
}

// Favorite adds a track to favorites. No-op if already present.
// Returns true when the track was newly added.
func (s *Store) Favorite(track playlist.Track) (bool, error) {
	if s == nil || strings.TrimSpace(track.Path) == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockFile()
	if err != nil {
		return false, err
	}
	defer func() { _ = unlock() }()

	entries, err := s.loadLocked()
	if err != nil {
		return false, fmt.Errorf("load favorites: %w", err)
	}

	if slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Track.Path == track.Path
	}) {
		return false, nil
	}

	entry := Entry{Track: track, FavoritedAt: time.Now()}
	entries = append([]Entry{entry}, entries...)
	return true, s.saveLocked(entries)
}

// Remove unfavorites a track by path. Returns true when the track was present
// and removed.
func (s *Store) Remove(path string) (bool, error) {
	if s == nil || strings.TrimSpace(path) == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockFile()
	if err != nil {
		return false, err
	}
	defer func() { _ = unlock() }()

	entries, err := s.loadLocked()
	if err != nil {
		return false, fmt.Errorf("load favorites: %w", err)
	}

	idx := slices.IndexFunc(entries, func(e Entry) bool {
		return e.Track.Path == path
	})
	if idx < 0 {
		return false, nil
	}

	entries = slices.Delete(entries, idx, idx+1)
	return true, s.saveLocked(entries)
}

// IsFavorited reports whether the given path is in the favorites store.
// Read helper: load errors report false rather than failing the caller.
func (s *Store) IsFavorited(path string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, func(e Entry) bool {
		return e.Track.Path == path
	})
}

// Count returns the number of favorited tracks.
// Read helper: load errors report 0 rather than failing the caller.
func (s *Store) Count() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return 0
	}
	return len(entries)
}

// Tracks returns all favorite tracks, newest-first, suitable for handing to a
// playlist.Playlist. The FavoritedAt timestamp is dropped.
func (s *Store) Tracks() ([]playlist.Track, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]playlist.Track, len(entries))
	for i, e := range entries {
		out[i] = e.Track
	}
	return out, nil
}

// Clear deletes the favorites file. Returns nil if the file does not exist.
func (s *Store) Clear() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := s.lockFile()
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	err = os.Remove(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove favorites: %w", err)
	}
	return nil
}

// lockFile serializes writers across cliamp processes: the per-instance
// mutex alone cannot stop two processes from rewriting the same file.
func (s *Store) lockFile() (func() error, error) {
	return fileutil.LockFile(s.path + ".lock")
}

func (s *Store) loadLocked() ([]Entry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read favorites: %w", err)
	}
	entries, err := parse(data)
	if err != nil {
		return entries, err
	}
	return entries, nil
}

func (s *Store) saveLocked(entries []Entry) error {
	var b strings.Builder
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		writeEntry(&b, e)
	}
	// WriteFileAtomic uses a unique temp file per write and fsyncs before
	// renaming, so a crash or a second process can never leave a torn or
	// half-clobbered favorites.toml behind.
	return fileutil.WriteFileAtomic(s.path, []byte(b.String()), 0o644)
}

func writeEntry(w io.Writer, e Entry) {
	fmt.Fprintln(w, "[[entry]]")
	fmt.Fprintf(w, "favorited_at = %q\n", e.FavoritedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "path = %q\n", e.Track.Path)
	fmt.Fprintf(w, "title = %q\n", e.Track.Title)
	if e.Track.Artist != "" {
		fmt.Fprintf(w, "artist = %q\n", e.Track.Artist)
	}
	if e.Track.Album != "" {
		fmt.Fprintf(w, "album = %q\n", e.Track.Album)
	}
	if e.Track.Genre != "" {
		fmt.Fprintf(w, "genre = %q\n", e.Track.Genre)
	}
	if e.Track.Year != 0 {
		fmt.Fprintf(w, "year = %d\n", e.Track.Year)
	}
	if e.Track.TrackNumber != 0 {
		fmt.Fprintf(w, "track_number = %d\n", e.Track.TrackNumber)
	}
	if e.Track.DurationSecs != 0 {
		fmt.Fprintf(w, "duration_secs = %d\n", e.Track.DurationSecs)
	}
	if e.Track.Feed {
		fmt.Fprintln(w, "feed = true")
	}
	if e.Track.Realtime {
		fmt.Fprintln(w, "realtime = true")
	}
	for k, v := range e.Track.ProviderMeta {
		fmt.Fprintf(w, "provider_meta.%s = %q\n", k, v)
	}
}

// parse skips unknown keys to keep the on-disk format forward-compatible.
func parse(data []byte) ([]Entry, error) {
	normalized, err := tomlutil.RemoveRepeatedKeys(data)
	if err != nil {
		return nil, fmt.Errorf("parse favorites TOML: %w", err)
	}
	var raw struct {
		Entries []favoriteTOML `toml:"entry"`
	}
	if err := tomlutil.Decode(normalized, &raw); err != nil {
		return nil, fmt.Errorf("decode favorites TOML: %w", err)
	}
	entries := make([]Entry, 0, len(raw.Entries))
	var invalid error
	for i, e := range raw.Entries {
		if strings.TrimSpace(e.Path) == "" {
			if invalid == nil {
				invalid = fmt.Errorf("favorite entry %d has empty path", i+1)
			}
			continue
		}
		at, _ := time.Parse(time.RFC3339, e.FavoritedAt)
		entries = append(entries, Entry{FavoritedAt: at, Track: playlist.Track{Path: e.Path, Title: e.Title, Artist: e.Artist, Album: e.Album, Genre: e.Genre, Year: e.Year, TrackNumber: e.TrackNumber, DurationSecs: e.DurationSecs, Feed: e.Feed, Realtime: e.Realtime, Stream: playlist.IsURL(e.Path), ProviderMeta: tomlutil.FlattenStringMap(e.ProviderMeta)}})
	}
	return entries, invalid
}

type favoriteTOML struct {
	FavoritedAt  string         `toml:"favorited_at"`
	Path         string         `toml:"path"`
	Title        string         `toml:"title"`
	Artist       string         `toml:"artist"`
	Album        string         `toml:"album"`
	Genre        string         `toml:"genre"`
	Year         int            `toml:"year"`
	TrackNumber  int            `toml:"track_number"`
	DurationSecs int            `toml:"duration_secs"`
	Feed         bool           `toml:"feed"`
	Realtime     bool           `toml:"realtime"`
	ProviderMeta map[string]any `toml:"provider_meta"`
}
