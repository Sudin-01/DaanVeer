package blockchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"github.com/Sudin-01/DaanVeer/wallet"
	"github.com/dgraph-io/badger/v4"
)

// Fund provenance: tracking earmarked donations through onward transfers.
//
// A donor gives to a cause, but the recipient is an ordinary account that can
// spend onward. The question a donor actually wants answered -- "where did my
// money go?" -- is not answerable from balances alone.
//
// Accounting model. This is an account-based ledger, so funds are fungible:
// once a charity holds donations from two campaigns, an onward payment is not
// intrinsically attributable to either. Exact tracing is only possible in a
// UTXO model, where outputs name their inputs. We therefore apply *proportional
// attribution* (the "haircut" method used in transaction forensics): a payment
// out of an account carries each campaign in proportion to that campaign's
// share of the account's holdings.
//
// This is an approximation and must be described as one in the paper. It is
// deterministic, order-independent, and conserves totals, but it does not
// recover an intent that the ledger never recorded. The alternatives -- FIFO
// and poison/taint -- are equally arbitrary; proportional attribution has the
// advantage that a mixed account reports mixed provenance rather than
// attributing everything to whichever donation happened to arrive first.

const (
	// CAMPAIGN_ID_LENGTH is the fixed width of a campaign identifier, which
	// keeps provenance keys prefix-scannable.
	CAMPAIGN_ID_LENGTH = 32

	// provByAccount indexes campaign -> amount for one account.
	provByAccount = "pa_"
	// provByCampaign indexes account -> amount for one campaign.
	provByCampaign = "pc_"
)

// CampaignID derives a campaign identifier from a human-readable name.
func CampaignID(name string) []byte {
	sum := sha256.Sum256([]byte("daanveer-campaign:" + name))
	return sum[:]
}

func accountProvKey(pubKeyHash, campaign []byte) []byte {
	key := make([]byte, 0, len(provByAccount)+len(pubKeyHash)+len(campaign))
	key = append(key, provByAccount...)
	key = append(key, pubKeyHash...)
	return append(key, campaign...)
}

func campaignProvKey(campaign, pubKeyHash []byte) []byte {
	key := make([]byte, 0, len(provByCampaign)+len(campaign)+len(pubKeyHash))
	key = append(key, provByCampaign...)
	key = append(key, campaign...)
	return append(key, pubKeyHash...)
}

// holding is one campaign's share of an account.
type holding struct {
	Campaign []byte
	Amount   uint64
}

// accountHoldings reads an account's provenance, sorted for determinism.
func accountHoldings(txn *badger.Txn, pubKeyHash []byte) ([]holding, uint64, error) {
	prefix := append([]byte(provByAccount), pubKeyHash...)
	opts := badger.DefaultIteratorOptions
	it := txn.NewIterator(opts)
	defer it.Close()

	var holdings []holding
	var total uint64
	for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
		item := it.Item()
		key := item.KeyCopy(nil)
		campaign := key[len(prefix):]

		var amount uint64
		if err := item.Value(func(val []byte) error {
			if len(val) != 8 {
				return fmt.Errorf("corrupt provenance entry: %d bytes", len(val))
			}
			amount = binary.BigEndian.Uint64(val)
			return nil
		}); err != nil {
			return nil, 0, err
		}
		if amount == 0 {
			continue
		}
		holdings = append(holdings, holding{Campaign: campaign, Amount: amount})
		total += amount
	}
	sort.Slice(holdings, func(i, j int) bool {
		return bytes.Compare(holdings[i].Campaign, holdings[j].Campaign) < 0
	})
	return holdings, total, nil
}

func adjustProvenance(txn *badger.Txn, pubKeyHash, campaign []byte, delta int64) error {
	accountKey := accountProvKey(pubKeyHash, campaign)
	campaignKey := campaignProvKey(campaign, pubKeyHash)

	current, err := readBalance(txn, accountKey)
	if err != nil {
		return err
	}
	var next uint64
	if delta < 0 {
		debit := uint64(-delta)
		if debit > current {
			debit = current // clamp: proportional rounding can overshoot by 1
		}
		next = current - debit
	} else {
		next = current + uint64(delta)
	}

	if next == 0 {
		if err := txn.Delete(accountKey); err != nil {
			return err
		}
		return txn.Delete(campaignKey)
	}
	if err := writeBalance(txn, accountKey, next); err != nil {
		return err
	}
	return writeBalance(txn, campaignKey, next)
}

// allocate splits `amount` across holdings in proportion to their size.
//
// Integer arithmetic with the remainder distributed in sorted campaign order,
// so every node computes the same split from the same state.
func allocate(holdings []holding, total, amount uint64) []holding {
	if total == 0 || amount == 0 {
		return nil
	}
	if amount >= total {
		return holdings
	}

	out := make([]holding, 0, len(holdings))
	var assigned uint64
	for _, h := range holdings {
		share := uint64((float64(h.Amount) / float64(total)) * float64(amount))
		if share > h.Amount {
			share = h.Amount
		}
		out = append(out, holding{Campaign: h.Campaign, Amount: share})
		assigned += share
	}

	// Hand any rounding remainder to the largest holdings first.
	for remainder := amount - assigned; remainder > 0; {
		progressed := false
		for i := range out {
			if remainder == 0 {
				break
			}
			if out[i].Amount < holdings[i].Amount {
				out[i].Amount++
				remainder--
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

// provenanceForBlock applies (sign +1) or reverts (sign -1) a block's effect on
// the provenance index.
func provenanceForBlock(txn *badger.Txn, blk *Block, sign int64) error {
	transactions := blk.Transactions()
	// Reverting must undo transactions in the opposite order to their
	// application, otherwise proportional splits are computed against the
	// wrong account composition.
	if sign < 0 {
		reversed := make([]Transactions, len(transactions))
		for i, tx := range transactions {
			reversed[len(transactions)-1-i] = tx
		}
		transactions = reversed
	}

	for _, tx := range transactions {
		if err := provenanceForTx(txn, tx, sign); err != nil {
			return err
		}
	}
	return nil
}

func provenanceForTx(txn *badger.Txn, tx Transactions, sign int64) error {
	earmarked := len(tx.CampaignID) == CAMPAIGN_ID_LENGTH

	// Genesis funds are unattributed unless explicitly earmarked.
	if tx.IsGenesis() {
		if !earmarked {
			return nil
		}
		return adjustProvenance(txn, tx.RecipientHash, tx.CampaignID, int64(tx.Value)*sign)
	}

	if earmarked {
		// An explicit earmark overrides the sender's existing composition:
		// the donor is declaring what this donation is for.
		if err := debitSender(txn, tx, sign); err != nil {
			return err
		}
		return adjustProvenance(txn, tx.RecipientHash, tx.CampaignID, int64(tx.Value)*sign)
	}

	// Unattributed onward transfer: carry the sender's composition forward in
	// proportion, so a charity forwarding mixed funds reports mixed provenance.
	holdings, total, err := accountHoldings(txn, tx.SenderHash)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil // sender holds nothing attributed; nothing to carry
	}
	for _, share := range allocate(holdings, total, tx.Value) {
		if share.Amount == 0 {
			continue
		}
		if err := adjustProvenance(txn, tx.SenderHash, share.Campaign, -int64(share.Amount)*sign); err != nil {
			return err
		}
		if err := adjustProvenance(txn, tx.RecipientHash, share.Campaign, int64(share.Amount)*sign); err != nil {
			return err
		}
	}
	return nil
}

// debitSender removes an earmarked payment from the sender's provenance,
// proportionally across whatever the sender holds.
func debitSender(txn *badger.Txn, tx Transactions, sign int64) error {
	holdings, total, err := accountHoldings(txn, tx.SenderHash)
	if err != nil {
		return err
	}
	if total == 0 {
		return nil
	}
	for _, share := range allocate(holdings, total, tx.Value) {
		if share.Amount == 0 {
			continue
		}
		if err := adjustProvenance(txn, tx.SenderHash, share.Campaign, -int64(share.Amount)*sign); err != nil {
			return err
		}
	}
	return nil
}

// CampaignHolding is one account's remaining share of a campaign.
type CampaignHolding struct {
	PubKeyHash []byte `json:"pub_key_hash"`
	Amount     uint64 `json:"amount"`
}

// TraceCampaign reports where a campaign's funds currently sit.
//
// One prefix scan over accounts holding that campaign -- proportional to the
// number of such accounts, not to chain length.
func (chain *BlockChain) TraceCampaign(campaign []byte) ([]CampaignHolding, uint64, error) {
	if len(campaign) != CAMPAIGN_ID_LENGTH {
		return nil, 0, errors.New("campaign id must be 32 bytes")
	}

	var holdings []CampaignHolding
	var total uint64

	err := chain.Database.View(func(txn *badger.Txn) error {
		prefix := append([]byte(provByCampaign), campaign...)
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := item.KeyCopy(nil)
			pubKeyHash := key[len(prefix):]

			var amount uint64
			if err := item.Value(func(val []byte) error {
				if len(val) != 8 {
					return fmt.Errorf("corrupt provenance entry: %d bytes", len(val))
				}
				amount = binary.BigEndian.Uint64(val)
				return nil
			}); err != nil {
				return err
			}
			if amount == 0 {
				continue
			}
			holdings = append(holdings, CampaignHolding{PubKeyHash: pubKeyHash, Amount: amount})
			total += amount
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	sort.Slice(holdings, func(i, j int) bool {
		if holdings[i].Amount != holdings[j].Amount {
			return holdings[i].Amount > holdings[j].Amount
		}
		return bytes.Compare(holdings[i].PubKeyHash, holdings[j].PubKeyHash) < 0
	})
	return holdings, total, nil
}

// AccountProvenance reports how an account's balance breaks down by campaign.
func (chain *BlockChain) AccountProvenance(address string) (map[string]uint64, error) {
	pubKeyHash, err := wallet.PubKeyFromAddress(address)
	if err != nil {
		return nil, fmt.Errorf("invalid address: %v", err)
	}

	out := map[string]uint64{}
	err = chain.Database.View(func(txn *badger.Txn) error {
		holdings, _, err := accountHoldings(txn, pubKeyHash)
		if err != nil {
			return err
		}
		for _, h := range holdings {
			out[fmt.Sprintf("%x", h.Campaign)] = h.Amount
		}
		return nil
	})
	return out, err
}
