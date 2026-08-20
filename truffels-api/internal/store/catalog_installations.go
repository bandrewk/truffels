package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type CatalogInstallation struct {
	ID        string         `json:"id"`
	CatalogID string         `json:"catalog_id"`
	Params    map[string]any `json:"params"`
	CreatedAt string         `json:"created_at"`
}

func (s *Store) AddCatalogInstallation(id, catalogID string, params map[string]any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO catalog_installations (id, catalog_id, params) VALUES (?, ?, ?)`,
		id, catalogID, string(raw))
	return err
}

func (s *Store) RemoveCatalogInstallation(id string) error {
	_, err := s.db.Exec(`DELETE FROM catalog_installations WHERE id = ?`, id)
	return err
}

func (s *Store) GetCatalogInstallation(id string) (CatalogInstallation, bool, error) {
	row := s.db.QueryRow(
		`SELECT id, catalog_id, params, created_at FROM catalog_installations WHERE id = ?`, id)
	ci, err := scanCatalogInstallation(row)
	if err == sql.ErrNoRows {
		return CatalogInstallation{}, false, nil
	}
	if err != nil {
		return CatalogInstallation{}, false, err
	}
	return ci, true, nil
}

func (s *Store) ListCatalogInstallations() ([]CatalogInstallation, error) {
	rows, err := s.db.Query(
		`SELECT id, catalog_id, params, created_at FROM catalog_installations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CatalogInstallation
	for rows.Next() {
		ci, err := scanCatalogInstallation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ci)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanCatalogInstallation(r rowScanner) (CatalogInstallation, error) {
	var ci CatalogInstallation
	var raw string
	if err := r.Scan(&ci.ID, &ci.CatalogID, &raw, &ci.CreatedAt); err != nil {
		return ci, err
	}
	if err := json.Unmarshal([]byte(raw), &ci.Params); err != nil {
		return ci, fmt.Errorf("unmarshal params: %w", err)
	}
	return ci, nil
}
