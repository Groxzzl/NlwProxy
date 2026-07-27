// Package profiles stores named provider configurations and durable selection metadata.
package profiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"nlwproxy/internal/config"
)

const indexFile = "index.json"

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

var (
	ErrNotFound          = errors.New("profile not found")
	ErrExists            = errors.New("profile already exists")
	ErrSelectionRequired = errors.New("profile selection required")
	ErrWizardRequired    = errors.New("profile setup required")
)

type Profile struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Config    config.Config `json:"config"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type Entry struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	APIKeyEnv string    `json:"api_key_env,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Index struct {
	Version  int     `json:"version"`
	Active   string  `json:"active,omitempty"`
	LastUsed string  `json:"last_used,omitempty"`
	Profiles []Entry `json:"profiles"`
}

type Store struct {
	dir string
	now func() time.Time
}

func Open(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("profiles directory is required")
	}
	clean, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{dir: clean, now: time.Now}
	if err := os.MkdirAll(clean, 0700); err != nil {
		return nil, err
	}
	if _, err := s.loadIndex(); errors.Is(err, os.ErrNotExist) {
		if err := s.writeIndex(Index{Version: 1, Profiles: []Entry{}}); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Dir() string { return s.dir }
func ValidateID(id string) error {
	if !idPattern.MatchString(id) {
		return errors.New("profile id must contain only lowercase letters, digits, underscore, or hyphen")
	}
	return nil
}
func (s *Store) profilePath(id string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	p := filepath.Join(s.dir, id+".json")
	rel, err := filepath.Rel(s.dir, p)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", errors.New("unsafe profile path")
	}
	return p, nil
}
func (s *Store) List() ([]Entry, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	return append([]Entry(nil), idx.Profiles...), nil
}
func (s *Store) Index() (Index, error) { return s.loadIndex() }
func (s *Store) Get(id string) (Profile, error) {
	p, err := s.profilePath(id)
	if err != nil {
		return Profile{}, err
	}
	f, err := os.Open(p)
	if os.IsNotExist(err) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	defer f.Close()
	var out Profile
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(&out); err != nil {
		return Profile{}, fmt.Errorf("decode profile: %w", err)
	}
	if out.ID != id {
		return Profile{}, errors.New("profile id does not match file")
	}
	if err = out.Config.Validate(); err != nil {
		return Profile{}, err
	}
	return out, nil
}
func (s *Store) Create(p Profile) (Profile, error) {
	if err := validateProfile(p); err != nil {
		return Profile{}, err
	}
	if _, err := s.Get(p.ID); err == nil {
		return Profile{}, ErrExists
	} else if !errors.Is(err, ErrNotFound) {
		return Profile{}, err
	}
	now := s.now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	if err := s.writeProfile(p); err != nil {
		return Profile{}, err
	}
	idx, err := s.loadIndex()
	if err != nil {
		return Profile{}, err
	}
	idx.Profiles = append(idx.Profiles, entryFor(p))
	sort.Slice(idx.Profiles, func(i, j int) bool { return idx.Profiles[i].Name < idx.Profiles[j].Name })
	if len(idx.Profiles) == 1 {
		idx.Active = p.ID
		idx.LastUsed = p.ID
	}
	if err = s.writeIndex(idx); err != nil {
		_ = os.Remove(filepath.Join(s.dir, p.ID+".json"))
		return Profile{}, err
	}
	return p, nil
}
func (s *Store) Update(id string, p Profile) (Profile, error) {
	old, err := s.Get(id)
	if err != nil {
		return Profile{}, err
	}
	if p.ID == "" {
		p.ID = id
	}
	if p.ID != id {
		return Profile{}, errors.New("profile id cannot be changed")
	}
	if err = validateProfile(p); err != nil {
		return Profile{}, err
	}
	p.CreatedAt = old.CreatedAt
	p.UpdatedAt = s.now().UTC()
	if err = s.writeProfile(p); err != nil {
		return Profile{}, err
	}
	idx, err := s.loadIndex()
	if err != nil {
		return Profile{}, err
	}
	for i := range idx.Profiles {
		if idx.Profiles[i].ID == id {
			idx.Profiles[i] = entryFor(p)
		}
	}
	sort.Slice(idx.Profiles, func(i, j int) bool { return idx.Profiles[i].Name < idx.Profiles[j].Name })
	return p, s.writeIndex(idx)
}
func (s *Store) Delete(id string) error {
	p, err := s.profilePath(id)
	if err != nil {
		return err
	}
	idx, err := s.loadIndex()
	if err != nil {
		return err
	}
	found := false
	out := idx.Profiles[:0]
	for _, e := range idx.Profiles {
		if e.ID == id {
			found = true
		} else {
			out = append(out, e)
		}
	}
	if !found {
		return ErrNotFound
	}
	if err = os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	idx.Profiles = out
	if idx.Active == id {
		idx.Active = ""
	}
	if idx.LastUsed == id {
		idx.LastUsed = ""
	}
	if len(out) == 1 {
		idx.Active = out[0].ID
		idx.LastUsed = out[0].ID
	}
	return s.writeIndex(idx)
}
func (s *Store) Activate(id string) (Profile, error) {
	p, err := s.Get(id)
	if err != nil {
		return Profile{}, err
	}
	idx, err := s.loadIndex()
	if err != nil {
		return Profile{}, err
	}
	idx.Active = id
	idx.LastUsed = id
	if err = s.writeIndex(idx); err != nil {
		return Profile{}, err
	}
	return p, nil
}
func (s *Store) Select() (Profile, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return Profile{}, err
	}
	switch len(idx.Profiles) {
	case 0:
		return Profile{}, ErrWizardRequired
	case 1:
		return s.Activate(idx.Profiles[0].ID)
	}
	if idx.Active != "" {
		if p, e := s.Get(idx.Active); e == nil {
			return s.Activate(p.ID)
		}
	}
	if idx.LastUsed != "" {
		if p, e := s.Get(idx.LastUsed); e == nil {
			return s.Activate(p.ID)
		}
	}
	return Profile{}, ErrSelectionRequired
}
func (s *Store) Migrate(legacyPath string) (Profile, bool, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return Profile{}, false, err
	}
	if len(idx.Profiles) > 0 {
		return Profile{}, false, nil
	}
	cfg, err := config.Load(legacyPath)
	if os.IsNotExist(err) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, err
	}
	if len(cfg.Upstreams) == 0 {
		return Profile{}, false, nil
	}
	up := cfg.Upstreams[0]
	id := slug(up.Name)
	if id == "" {
		id = "provider"
	}
	base := id
	for n := 2; ; n++ {
		if _, e := s.Get(id); errors.Is(e, ErrNotFound) {
			break
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
	p, err := s.Create(Profile{ID: id, Name: up.Name, Config: cfg})
	return p, err == nil, err
}
func slug(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	dash := false
	for _, r := range v {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
func validateProfile(p Profile) error {
	if err := ValidateID(p.ID); err != nil {
		return err
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("profile name is required")
	}
	if err := p.Config.Validate(); err != nil {
		return err
	}
	return nil
}
func entryFor(p Profile) Entry {
	env := ""
	if len(p.Config.Upstreams) > 0 {
		env = p.Config.Upstreams[0].APIKeyEnv
	}
	return Entry{ID: p.ID, Name: p.Name, APIKeyEnv: env, UpdatedAt: p.UpdatedAt}
}
func (s *Store) loadIndex() (Index, error) {
	f, err := os.Open(filepath.Join(s.dir, indexFile))
	if err != nil {
		return Index{}, err
	}
	defer f.Close()
	var idx Index
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err = d.Decode(&idx); err != nil {
		return Index{}, fmt.Errorf("decode profile index: %w", err)
	}
	if idx.Version != 1 {
		return Index{}, errors.New("unsupported profile index version")
	}
	seen := map[string]bool{}
	for _, e := range idx.Profiles {
		if ValidateID(e.ID) != nil || seen[e.ID] {
			return Index{}, errors.New("invalid profile index")
		}
		seen[e.ID] = true
	}
	return idx, nil
}
func (s *Store) writeProfile(p Profile) error {
	path, err := s.profilePath(p.ID)
	if err != nil {
		return err
	}
	return writeJSON(path, p)
}
func (s *Store) writeIndex(idx Index) error {
	idx.Version = 1
	if idx.Profiles == nil {
		idx.Profiles = []Entry{}
	}
	return writeJSON(filepath.Join(s.dir, indexFile), idx)
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".profiles-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(b)
	}
	if ce := tmp.Close(); err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		if re := os.Remove(path); re != nil && !os.IsNotExist(re) {
			return err
		}
		err = os.Rename(name, path)
	}
	return err
}
