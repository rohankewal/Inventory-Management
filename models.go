package main

import (
	"fmt"
)

type Product struct {
	ID    int
	Name  string
	Price float64
	Stock int
}

func (p Product) String() string {
	return fmt.Sprintf("ID: %04d | Product: %-15s | Price: $%6.2f | Stock: %d",
		p.ID, p.Name, p.Price, p.Stock)
}
