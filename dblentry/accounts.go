package dblentry

import "fmt"
import "sort"
import "strings"

import "github.com/prataprc/goparsec"
import "github.com/tn47/goledger/api"

var inclusives = []string{
	"asset", "liability", "capital", "equity", "income", "expense",
}

// Account implements api.Accounter{} interface.
type Account struct {
	name       string
	virtual    bool
	balanced   bool
	hasposting bool
	balance    map[string]*Commodity
	// from account directive
	notes   []string
	aliases []string
	payees  []string
}

// NewAccount create a new instance of Account{}.
func NewAccount(name string) *Account {
	acc := &Account{
		name:    name,
		balance: make(map[string]*Commodity),
	}
	return acc
}

//---- local accessors

func (acc *Account) setPosting() {
	acc.hasposting = true
}

func (acc *Account) setOpeningbalance(commodity *Commodity) *Account {
	acc.balance[commodity.name] = commodity.makeSimilar(commodity.amount)
	return acc
}

func (acc *Account) isVirtual() bool {
	return acc.virtual
}

func (acc *Account) addNote(note string) *Account {
	if note != "" {
		acc.notes = append(acc.notes, note)
	}
	return acc
}

func (acc *Account) addAlias(alias string) *Account {
	if alias != "" {
		acc.aliases = append(acc.aliases, alias)
	}
	return acc
}

func (acc *Account) addPayee(payee string) *Account {
	if payee != "" {
		acc.payees = append(acc.payees, payee)
	}
	return acc
}

func (acc *Account) isUnknown() bool {
	if acc.name == "Unknown" || strings.HasSuffix(acc.name, ":Unknown") {
		return true
	}
	return false
}

//---- api.Accounter methods.

func (acc *Account) Name() string {
	return acc.name
}

func (acc *Account) Balance(obj interface{}) (balance api.Commoditiser) {
	switch v := obj.(type) {
	case *Commodity:
		balance, _ = acc.balance[v.name]
	case string:
		balance, _ = acc.balance[v]
	}
	return balance
}

func (acc *Account) Balances() []api.Commoditiser {
	keys := []string{}
	for name := range acc.balance {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	comms := []api.Commoditiser{}
	for _, key := range keys {
		comms = append(comms, acc.balance[key])
	}
	return comms
}

func (acc *Account) Balanced() bool {
	return acc.balanced
}

func (acc *Account) HasPosting() bool {
	return acc.hasposting
}

func (acc *Account) String() string {
	return fmt.Sprintf("%v", acc.name)
}

//---- api.Reporter{} methods

func (acc *Account) FmtBalances(
	db api.Datastorer, trans api.Transactor, p api.Poster,
	_ api.Accounter) [][]string {

	if len(acc.Balances()) == 0 {
		return nil
	}

	rows := make([][]string, 0)
	for _, balance := range acc.Balances() {
		if balance.Amount() != 0 || acc.HasPosting() == false {
			rows = append(rows, []string{"", "", balance.String()})
		}
	}
	if len(rows) > 0 { // last row to include date and account name.
		lastrow := rows[len(rows)-1]
		date := trans.Date().Format("2006/Jan/02")
		lastrow[0], lastrow[1] = date, acc.Name()
	}
	return rows
}

func (acc *Account) FmtEquity(
	db api.Datastorer, trans api.Transactor, p api.Poster,
	_ api.Accounter) [][]string {

	if len(acc.Balances()) == 0 {
		return nil
	}

	var rows [][]string

	for _, balance := range acc.Balances() {
		if balance.Amount() != 0 {
			rows = append(rows, []string{"", acc.Name(), balance.String()})
		}
	}
	return rows
}

func (acc *Account) FmtRegister(
	db api.Datastorer, trans api.Transactor, p api.Poster,
	_ api.Accounter) [][]string {

	panic("not supported")
}

//---- ledger parser

// Yledger return a parser-combinator that can parse an account name.
func (acc *Account) Yledger(db *Datastore) parsec.Parser {
	y := parsec.OrdChoice(
		func(nodes []parsec.ParsecNode) parsec.ParsecNode {
			t := nodes[0].(*parsec.Terminal)
			name := strings.Trim(string(t.Value), " \t")
			switch t.Name {
			case "FULLACCNM":
				acc.name = name
				acc.virtual, acc.balanced = false, true
				return acc
			case "VFULLACCNM":
				acc.name = name[1 : len(name)-1]
				acc.virtual, acc.balanced = true, false
				return acc
			case "BFULLACCNM":
				acc.name = name[1 : len(name)-1]
				acc.virtual, acc.balanced = true, true
				return acc
			}
			panic(fmt.Errorf("unreachable code: terminal(%q)", t.Name))
		},
		ytokAccname, ytokVaccname, ytokBaccname,
	)
	return y
}

// Ypostaccn return a parser-combinator that parses an account name in the
// context of a posting.
func (acc *Account) Ypostaccn(db *Datastore) parsec.Parser {
	yacconly := parsec.And(
		func(nodes []parsec.ParsecNode) parsec.ParsecNode {
			return nodes[0]
		},
		ytokAccname, parsec.Parser(parsec.End()),
	)
	y := parsec.OrdChoice(
		func(nodes []parsec.ParsecNode) parsec.ParsecNode {
			t := nodes[0].(*parsec.Terminal)
			name := strings.Trim(string(t.Value), " \t")
			switch t.Name {
			case "POSTACCN1":
				acc.name = name
				acc.virtual, acc.balanced = false, true
				return acc
			case "FULLACCNM":
				acc.name = name
				acc.virtual, acc.balanced = false, true
				return acc
			case "VFULLACCNM":
				acc.name = name[1 : len(name)-1]
				acc.virtual, acc.balanced = true, false
				return acc
			case "BFULLACCNM":
				acc.name = name[1 : len(name)-1]
				acc.virtual, acc.balanced = true, true
				return acc
			}
			panic(fmt.Errorf("unreachable code: terminal(%q)", t.Name))
		},
		ytokPostacc1, ytokVaccname, ytokBaccname, yacconly,
	)
	return y
}

//---- engine

func (acc *Account) Firstpass(
	db *Datastore, trans *Transaction, p *Posting) error {

	return nil
}

func (acc *Account) Secondpass(
	db *Datastore, trans *Transaction, p *Posting) error {

	p.account.addBalance(p.commodity)

	balance := p.account.Balance(p.commodity.name)
	if p.balprice != nil && balance.BalanceEqual(p.balprice) == false {
		accname := p.account.name
		fmsg := "account(%v) should balance as %s, got %s\n"
		return fmt.Errorf(fmsg, accname, p.balprice.String(), balance.String())
	}

	if err := db.reporter.Posting(db, trans, p); err != nil {
		return err
	}
	if err := acc.doTotal(db, trans, p); err != nil {
		return err
	}
	return nil
}

func (acc *Account) Clone(ndb *Datastore) *Account {
	nacc := *acc
	nacc.balance = map[string]*Commodity{}
	for name, commodity := range acc.balance {
		nacc.balance[name] = commodity.Clone(ndb)
	}
	return &nacc
}

func (acc *Account) doTotal(db *Datastore, trans *Transaction, p *Posting) error {
	names := SplitAccount(acc.name)
	parts := []string{}
	for _, name := range names[:len(names)-1] {
		parts = append(parts, name)
		fullname := strings.Join(parts, ":")
		consacc := db.GetAccount(fullname).(*Account)
		consacc.addBalance(p.commodity)
		err := db.reporter.BubblePosting(db, trans, p, consacc)
		if err != nil {
			return err
		}
	}
	return nil
}

func (acc *Account) addBalance(commodity *Commodity) {
	if balance, ok := acc.balance[commodity.name]; ok {
		balance.amount += commodity.amount
		acc.balance[commodity.name] = balance
		return
	}
	acc.balance[commodity.name] = commodity.makeSimilar(commodity.amount)
}

func (acc *Account) deductBalance(commodity *Commodity) {
	if balance, ok := acc.balance[commodity.name]; ok {
		balance.amount -= commodity.amount
		acc.balance[commodity.name] = balance
		return
	}
	acc.balance[commodity.name] = commodity.makeSimilar(commodity.amount)
}

// FitAccountname for formatting.
func FitAccountname(name string, maxwidth int) string {
	if len(name) < maxwidth {
		return name
	}
	scraplen := maxwidth - len(name)
	names := []string{}
	for _, name := range SplitAccount(name) {
		if scraplen <= 0 {
			names = append(names, name)
		}
		if len(name[3:]) < scraplen {
			names = append(names, name[:3])
			scraplen -= len(name[3:])
			continue
		}
		names = append(names, name[:len(name)-scraplen])
		scraplen = 0
	}
	return JoinAccounts(names)
}

// SplitAccount into segments.
func SplitAccount(name string) []string {
	return strings.Split(strings.Trim(name, ":"), ":")
}

// JoinAccounts from segments.
func JoinAccounts(segments []string) string {
	return strings.Join(segments, ":")
}
