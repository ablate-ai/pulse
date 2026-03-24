package nodes

import "errors"

var ErrNodeNotFound = errors.New("node not found")

type Node struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

type Store interface {
	Upsert(node Node) (Node, error)
	Delete(id string) error
	Get(id string) (Node, error)
	List() ([]Node, error)
}
