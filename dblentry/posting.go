package dblentry

import "fmt"
import "time"
import "strings"

import "github.com/prataprc/goparsec"
import "github.com/prataprc/golog"
import "github.com/tn47/goledger/api"

const (
	// PostUncleared notion for a posting.
	PostUncleared = "uncleared"
	// PostCleared notion for a posting.
	PostCleared = "cleared"
	// PostPending notion for a posting.
	PostPending = "pending"
)

var prefix2state = map[rune]string{
	'*': PostCleared,
	'!': PostPending,
}

// Posting instance for every single posting within a transaction.
type Posting struct {
	trans     *Transaction
	account   *Account
	virtual   bool
	balanced  bool
	commodity *Commodity

	lotprice  *Commodity
	lotdate   time.Time
	costprice *Commodity
	balprice  *Commodity

	tags     []string
	metadata map[string]interface{}
	note     string
}

// NewPosting create a new posting instance.
func NewPosting(trans *Transaction) *Posting {
	return &Posting{
		trans:    trans,
		tags:     []string{},
		metadata: map[string]interface{}{},
	}
}

//---- local accessors

func (p *Posting) getMetadata(key string) interface{} {
	if value, ok := p.metadata[key]; ok {
		return value
	}
	return p.trans.getMetadata(key)
}

func (p *Posting) setMetadata(key string, value interface{}) {
	p.metadata[key] = value
}

func (p *Posting) isVirtual() bool {
	return p.virtual
}

func (p *Posting) isBalanced() bool {
	return p.balanced
}

//---- api.Poster methods.

func (p *Posting) Account() api.Accounter {
	return p.account
}

func (p *Posting) Commodity() api.Commoditiser {
	return p.commodity
}

func (p *Posting) Payee() string {
	payee := p.getMetadata("payee")
	if payee == nil {
		return ""
	}
	return payee.(string)
}

func (p *Posting) getState() string {
	state := p.getMetadata("state")
	if state == nil {
		return ""
	}
	return state.(string)
}

//---- ledger parser

// Yledger return parser-combinator that can parse a posting line within
// a transaction.
func (p *Posting) Yledger(db *Datastore) parsec.Parser {
	account := NewAccount("")
	comm := NewCommodity("")
	lotprice := NewCommodity("")
	costprice := NewCommodity("")
	balprice := NewCommodity("")

	ylotdate := parsec.And(
		nil,
		ytokOpensqrt, Ydate(db.getYear()), ytokClosesqrt,
	)

	yposting := parsec.And(
		nil,
		parsec.Maybe(maybenode, ytokPrefix),
		account.Ypostaccn(db),
		parsec.Maybe(maybenode, comm.Yledger(db)),
		parsec.Maybe(maybenode, lotprice.Ylotprice(db)),
		parsec.Maybe(maybenode, ylotdate),
		parsec.Maybe(maybenode, costprice.Ycostprice(db)),
		parsec.Maybe(maybenode, balprice.Ybalprice(db)),
		parsec.Maybe(maybenode, ytokPostnote),
	)

	y := parsec.OrdChoice(
		func(nodes []parsec.ParsecNode) parsec.ParsecNode {
			switch items := nodes[0].(type) {
			case []parsec.ParsecNode:
				// prefix
				if t, ok := items[0].(*parsec.Terminal); ok {
					p.setMetadata("state", prefix2state[[]rune(t.Value)[0]])
				}

				p.account, p.virtual, p.balanced = p.fixaccount(db, items[1])
				p.commodity = p.fixcommodity(db, items[2]) // commodity
				p.lotprice = p.fixlotprice(items[3])       // lot price
				p.lotdate = p.fixlotdate(items[4])         // lot date
				p.costprice = p.fixcostprice(items[5])     // cost price
				p.balprice = p.fixbalprice(items[6])       // balance price

				if p.lotprice != nil && lotprice.currency == false {
					return fmt.Errorf("lot price must be currency")
				} else if p.costprice != nil && costprice.currency == false {
					return fmt.Errorf("cost price must be currency")
				}
				if x, y := p.balprice, p.commodity; x != nil && y != nil {
					if x.name != y.name {
						fmsg := "balance-commodity(%v) != posting-commodity(%v)"
						return fmt.Errorf(fmsg, x.name, y.name)
					}
				}

				// optionally tags or tagkv or note
				if note, ok := items[7].(*parsec.Terminal); ok {
					scanner := parsec.NewScanner([]byte(note.Value))
					if node, _ := NewTag().Yledger(db)(scanner); node == nil {
						p.note = string(note.Value)

					} else {
						tag := node.(*Tags)
						p.tags = append(p.tags, tag.tags...)
						for k, v := range tag.tagm {
							p.setMetadata(k, v)
						}
					}
				}

				fmsg := "posting.yledger account:%v commodity:%v %v\n"
				log.Debugf(fmsg, p.account, p.commodity, p.costprice)
				return p

			case *parsec.Terminal:
				inp := []byte(strings.TrimLeft(items.Value, ";"))
				scanner := parsec.NewScanner(inp)
				node, _ := NewTag().Yledger(db)(scanner)
				if node == nil {
					log.Debugf("posting.yledger %v\n", string(items.Value))
					return typeTransnote(items.Value)
				}
				log.Debugf("posting.yledger %v\n", node)
				return node.(*Tags)
			}
			fmsg := "unreachable code posting: len(nodes): %v"
			panic(fmt.Errorf(fmsg, len(nodes)))
		},
		yposting,
		ytokTransnote,
	)
	return y
}

//---- engine

func (p *Posting) fixaccount(
	db *Datastore, item interface{}) (*Account, bool, bool) {

	account := item.(*Account)
	return account, account.virtual, account.balanced
}

func (p *Posting) fixcommodity(db *Datastore, item interface{}) *Commodity {
	if commodity, ok := item.(*Commodity); ok {
		return db.getCommodity(
			commodity.name, commodity,
		).makeSimilar(commodity.amount)
	}
	return nil
}

func (p *Posting) fixlotprice(item interface{}) *Commodity {
	if lotprice, ok := item.(*Commodity); ok {
		return lotprice
	}
	return nil
}

func (p *Posting) fixlotdate(item interface{}) (tm time.Time) {
	if lotnodes, ok := item.([]parsec.ParsecNode); ok {
		return lotnodes[1].(time.Time)
	}
	return
}

func (p *Posting) fixcostprice(item interface{}) *Commodity {
	if costprice, ok := item.(*Commodity); ok {
		return costprice
	}
	return nil
}

func (p *Posting) fixbalprice(item interface{}) *Commodity {
	if balprice, ok := item.(*Commodity); ok {
		return balprice
	}
	return nil
}

func (p *Posting) getCostprice() *Commodity {
	checkdebit := p.isDebit() && p.commodity.currency == false
	if checkdebit && p.costprice != nil {
		if p.costprice.isTotal() { // first compute per unit price
			p.costprice.amount /= p.commodity.amount
		}
		return p.costprice.makeSimilar(p.commodity.amount * p.costprice.amount)

	}

	checkcredit := p.isCredit() && p.commodity.currency == false
	if checkcredit && p.lotprice != nil {
		if p.lotprice.isTotal() { // first compute per unit price
			p.lotprice.amount /= p.commodity.amount
		}
		return p.lotprice.makeSimilar(p.commodity.amount * p.lotprice.amount)
	}

	return p.commodity.makeSimilar(p.commodity.amount)
}

func (p *Posting) isCredit() bool {
	if p.commodity == nil {
		panic("impossible situation")
	}
	if p.commodity.amount < 0 {
		return true
	}
	return false
}

func (p *Posting) isDebit() bool {
	return !p.isCredit()
}

func (p *Posting) Firstpass(db *Datastore, trans *Transaction) error {
	accname := p.account.name

	// if account is Unknown, try rewrite !!
	if p.account.isUnknown() {
		// fetch the declared account name with payee
		daccname, ok := db.matchpayee(trans.Payee())
		if ok == false {
			fmsg := "Unknown account %q has no matching payee %q\n"
			return fmt.Errorf(fmsg, p.account.name, trans.Payee())
		}
		prefix := p.account.name[:len(p.account.name)-len("Unknown")]
		if strings.HasPrefix(daccname, prefix) == false {
			fmsg := "Unknown account %q has no matching prefix %q\n"
			return fmt.Errorf(fmsg, p.account.name, accname)
		}
		accname = prefix + daccname[len(prefix):]

	} else {
		accname = db.applyroot(db.lookupAlias(accname))
	}

	if db.IsStrict() && db.HasAccount(accname) == false {
		return fmt.Errorf("account %q not declared before\n", accname)
	}

	p.account = db.GetAccount(accname).(*Account)

	if err := p.account.Firstpass(db, trans, p); err != nil {
		return err
	}
	if err := p.commodity.Firstpass(db, trans, p); err != nil {
		return err
	}
	return nil
}

func (p *Posting) Secondpass(db *Datastore, trans *Transaction) error {

	db.addBalance(p.commodity)
	p.account.setPosting()

	if err := p.account.Secondpass(db, trans, p); err != nil {
		return err
	}
	if err := p.commodity.Secondpass(db, trans, p); err != nil {
		return err
	}

	return nil
}

func (p *Posting) Clone(ndb *Datastore, ntrans *Transaction) *Posting {
	np := *p
	np.trans = ntrans
	np.account = ndb.GetAccount(p.account.name).(*Account)
	np.commodity = p.commodity.Clone(ndb)
	if p.lotprice != nil {
		np.lotprice = p.lotprice.Clone(ndb)
	}
	if p.costprice != nil {
		np.costprice = p.costprice.Clone(ndb)
	}
	if p.balprice != nil {
		np.balprice = p.balprice.Clone(ndb)
	}
	return &np
}

//---- api.Reporter methods

func (p *Posting) FmtBalances(
	db api.Datastorer, trans api.Transactor, _ api.Poster,
	_ api.Accounter) [][]string {

	panic("not supported")
}

func (p *Posting) FmtRegister(
	db api.Datastorer, trans api.Transactor, _ api.Poster,
	_ api.Accounter) [][]string {

	panic("not supported")
}

func (p *Posting) FmtEquity(
	db api.Datastorer, trans api.Transactor, _ api.Poster,
	_ api.Accounter) [][]string {

	panic("not supported")
}
