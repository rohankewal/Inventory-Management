package core

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Field limits for customers and their stores.
const (
	MaxCustomerCodeLen = 24
	MaxCustomerNameLen = 200
	MaxStoreCodeLen    = 32
	MaxTermsLen        = 120
	MaxRoutingNotesLen = 4000
)

// Customer is a client business that buys through you.
//
// It is distinct from a Location: a Location is a warehouse of yours where
// stock physically sits, while a Customer's stores are destinations goods are
// sent to. Conflating the two would make "on hand" meaningless.
type Customer struct {
	ID   ID
	Code string // short reference used on documents, e.g. "MACYS"
	Name string

	// Currency is what this customer is billed in, which need not be the
	// currency their goods were sourced in.
	Currency Currency
	// Terms is free text such as "Net 60" until payment handling exists.
	Terms   string
	Contact Contact
	// BillTo is the head-office address; goods go to store addresses instead.
	BillTo Address
	Notes  string

	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// Normalize trims and upper-cases the code.
func (c *Customer) Normalize() {
	c.Code = strings.ToUpper(strings.TrimSpace(c.Code))
	c.Name = strings.TrimSpace(c.Name)
	c.Terms = strings.TrimSpace(c.Terms)
	c.Notes = strings.TrimSpace(c.Notes)
	c.Contact.Normalize()
	c.BillTo.Normalize()

	if c.Currency == "" {
		c.Currency = DefaultCurrency
	}
	c.Currency = c.Currency.Normalize()
}

// Validate reports every problem at once.
func (c *Customer) Validate() error {
	var v ValidationError

	if c.Code == "" {
		v.Add("code", "Code is required")
	} else if utf8.RuneCountInString(c.Code) > MaxCustomerCodeLen {
		v.Add("code", "Code must be %d characters or fewer", MaxCustomerCodeLen)
	}
	if c.Name == "" {
		v.Add("name", "Name is required")
	} else if utf8.RuneCountInString(c.Name) > MaxCustomerNameLen {
		v.Add("name", "Name must be %d characters or fewer", MaxCustomerNameLen)
	}
	if !c.Currency.Valid() {
		v.Add("currency", "%q is not a valid currency code", c.Currency)
	}
	if utf8.RuneCountInString(c.Terms) > MaxTermsLen {
		v.Add("terms", "Terms must be %d characters or fewer", MaxTermsLen)
	}
	if utf8.RuneCountInString(c.Notes) > MaxNotesLen {
		v.Add("notes", "Notes must be %d characters or fewer", MaxNotesLen)
	}

	c.Contact.Validate("", &v)
	c.BillTo.Validate("bill_to_", &v)

	return v.ErrOrNil()
}

// CustomerStore is one ship-to destination belonging to a customer.
type CustomerStore struct {
	ID         ID
	CustomerID ID
	// Code is the customer's own store number, e.g. "0047". It is unique
	// within the customer, not globally: two retailers both having a store 001
	// is entirely normal.
	Code string
	Name string

	ShipTo  Address
	Contact Contact

	// RoutingNotes carries the customer's delivery requirements — appointment
	// booking, carton labelling, delivery windows. Large retailers charge back
	// for getting these wrong, so they belong on the shipping paperwork rather
	// than in somebody's inbox.
	RoutingNotes string

	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// Normalize trims and upper-cases the code.
func (s *CustomerStore) Normalize() {
	s.Code = strings.ToUpper(strings.TrimSpace(s.Code))
	s.Name = strings.TrimSpace(s.Name)
	s.RoutingNotes = strings.TrimSpace(s.RoutingNotes)
	s.ShipTo.Normalize()
	s.Contact.Normalize()
}

// Validate reports every problem at once.
func (s *CustomerStore) Validate() error {
	var v ValidationError

	if s.CustomerID.IsZero() {
		v.Add("customer_id", "A store must belong to a customer")
	}
	if s.Code == "" {
		v.Add("code", "Store code is required")
	} else if utf8.RuneCountInString(s.Code) > MaxStoreCodeLen {
		v.Add("code", "Store code must be %d characters or fewer", MaxStoreCodeLen)
	}
	if s.Name == "" {
		v.Add("name", "Store name is required")
	} else if utf8.RuneCountInString(s.Name) > MaxCustomerNameLen {
		v.Add("name", "Store name must be %d characters or fewer", MaxCustomerNameLen)
	}
	if utf8.RuneCountInString(s.RoutingNotes) > MaxRoutingNotesLen {
		v.Add("routing_notes", "Routing notes must be %d characters or fewer", MaxRoutingNotesLen)
	}

	s.ShipTo.Validate("ship_to_", &v)
	s.Contact.Validate("", &v)

	return v.ErrOrNil()
}

// Label renders the store the way it is referred to in conversation.
func (s CustomerStore) Label() string {
	if s.Name == "" {
		return s.Code
	}
	return s.Code + " — " + s.Name
}

// CustomerStoreCount pairs a customer with how many stores it has, for the
// customer list.
type CustomerWithStores struct {
	Customer
	StoreCount   int
	ActiveStores int
	OpenOrders   int
}
