package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type LatestDocument struct {
	Title     string    `json:"title"`
	Timestamp time.Time `json:"timestamp"`
}

type Storage struct {
	filePath string
	latest   LatestDocument
}

func New(filePath string) (*Storage, error) {
	s := &Storage{filePath: filePath}
	err := s.load()
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("error loading storage: %v", err)
		}
		// If file doesn't exist, initialize with empty document
		s.latest = LatestDocument{}
	}
	return s, nil
}

func (s *Storage) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.latest)
}

func (s *Storage) save() error {
	data, err := json.Marshal(s.latest)
	if err != nil {
		return fmt.Errorf("error marshaling data: %v", err)
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *Storage) UpdateLatest(title string, timestamp time.Time) error {
	s.latest = LatestDocument{Title: title, Timestamp: timestamp}
	return s.save()
}

func (s *Storage) GetLatest() LatestDocument {
	return s.latest
}
