package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type ProcessedDocument struct {
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	Timestamp time.Time `json:"timestamp"`
}

type StorageData struct {
	ProcessedDocs []ProcessedDocument `json:"processed_docs"`
}

type Storage struct {
	filePath string
	data     StorageData
	mu       sync.RWMutex
}

func New(filePath string) (*Storage, error) {
	s := &Storage{filePath: filePath}
	err := s.load()
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("error loading storage: %v", err)
		}
		// If file doesn't exist, initialize with empty data
		s.data = StorageData{
			ProcessedDocs: []ProcessedDocument{},
		}
	}
	return s, nil
}

func (s *Storage) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return json.Unmarshal(data, &s.data)
}

func (s *Storage) save() error {
	// Create a copy of the data under read lock
	s.mu.RLock()
	data, err := json.Marshal(s.data)
	s.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("error marshaling data: %v", err)
	}

	// Write to file outside of any lock to avoid IO-related deadlocks
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *Storage) AddProcessedDocument(doc ProcessedDocument) error {
	// Check if doc already exists and prepare data update under lock
	s.mu.Lock()

	// Check if doc already exists
	for _, existingDoc := range s.data.ProcessedDocs {
		if existingDoc.URL == doc.URL {
			s.mu.Unlock()
			return nil // Already processed
		}
	}

	// Prepend the new doc to keep most recent first
	s.data.ProcessedDocs = append([]ProcessedDocument{doc}, s.data.ProcessedDocs...)

	// Keep only the most recent X documents
	const maxDocsToKeep = 50
	if len(s.data.ProcessedDocs) > maxDocsToKeep {
		s.data.ProcessedDocs = s.data.ProcessedDocs[:maxDocsToKeep]
	}

	// Create a copy of data for saving
	dataCopy := s.data
	s.mu.Unlock()

	// Marshal and save the copy outside of lock
	jsonData, err := json.Marshal(dataCopy)
	if err != nil {
		return fmt.Errorf("error marshaling data: %v", err)
	}

	return os.WriteFile(s.filePath, jsonData, 0644)
}

func (s *Storage) IsDocumentProcessed(url string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, doc := range s.data.ProcessedDocs {
		if doc.URL == url {
			return true
		}
	}
	return false
}

// For backward compatibility
func (s *Storage) GetLatest() ProcessedDocument {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.ProcessedDocs) > 0 {
		return s.data.ProcessedDocs[0]
	}
	return ProcessedDocument{}
}

// For backward compatibility
func (s *Storage) UpdateLatest(title string, timestamp time.Time) error {
	return s.AddProcessedDocument(ProcessedDocument{
		Title:     title,
		Timestamp: timestamp,
	})
}
