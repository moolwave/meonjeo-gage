// Package store 는 수집한 업소를 JSON 파일 하나에 저장한다.
//
// 한 지역·몇 개 업종(수천~수만 건)까지는 이걸로 충분하다.
// 전국 단위로 커지면 같은 메서드를 가진 SQLite 버전으로 바꾸면 된다.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/moolwave/meonjeo-gage/localdata"
)

type Record struct {
	localdata.Business
	FirstSeen    time.Time `json:"firstSeen"`              // 우리가 처음 수집한 시각
	Category     string    `json:"category,omitempty"`     // AI/규칙 분류 결과
	Pitch        string    `json:"pitch,omitempty"`        // 영업 포인트 한 줄
	ClassifiedBy string    `json:"classifiedBy,omitempty"` // "ai" 또는 "rule"
}

type Store struct {
	path string
	recs map[string]*Record
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, recs: map[string]*Record{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []*Record
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	for _, r := range list {
		s.recs[r.Key()] = r
	}
	return s, nil
}

// Upsert 는 새 업소면 추가하고 true 를, 이미 있으면 최신 정보로 갱신하고 false 를 돌려준다.
// 분류 결과와 최초 수집 시각은 유지한다.
func (s *Store) Upsert(b localdata.Business, now time.Time) bool {
	if r, ok := s.recs[b.Key()]; ok {
		r.Business = b
		return false
	}
	s.recs[b.Key()] = &Record{Business: b, FirstSeen: now}
	return true
}

func (s *Store) All() []*Record {
	out := make([]*Record, 0, len(s.recs))
	for _, r := range s.recs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PermitDate != out[j].PermitDate {
			return out[i].PermitDate > out[j].PermitDate
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Store) Unclassified(limit int) []*Record {
	var out []*Record
	for _, r := range s.All() {
		if r.Category == "" {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (s *Store) Len() int { return len(s.recs) }

// Save 는 임시 파일에 쓴 뒤 이름을 바꿔서, 중간에 꺼져도 파일이 깨지지 않게 한다.
func (s *Store) Save() error {
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(s.All(), "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
