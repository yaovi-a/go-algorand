// Copyright (C) 2019-2021 Algorand, Inc.
// This file is part of go-algorand
//
// go-algorand is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// go-algorand is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with go-algorand.  If not, see <https://www.gnu.org/licenses/>.

package txnsync

import (
	"errors"
	"math"

	"github.com/algorand/go-algorand/data/transactions"
	"github.com/algorand/go-algorand/util/bloom"
)

const bloomFilterFalsePositiveRate = 0.01

var errInvalidBloomFilterEncoding = errors.New("invalid bloom filter encoding")

//msgp:ignore bloomFilterTypes
type bloomFilterTypes byte

const (
	invalidBloomFilter bloomFilterTypes = iota
	multiHashBloomFilter
	// xorBloomFilter - todo.
)

type transactionsRange struct {
	firstCounter      uint64
	lastCounter       uint64
	transactionsCount uint64
}
type bloomFilter struct {
	encodingParams requestParams

	filter *bloom.Filter

	containedTxnsRange transactionsRange
}

func decodeBloomFilter(enc encodedBloomFilter) (outFilter bloomFilter, err error) {
	switch bloomFilterTypes(enc.BloomFilterType) {
	case multiHashBloomFilter:
	default:
		return bloomFilter{}, errInvalidBloomFilterEncoding
	}

	outFilter.filter, err = bloom.UnmarshalBinary(enc.BloomFilter)
	if err != nil {
		return bloomFilter{}, err
	}
	outFilter.encodingParams = enc.EncodingParams
	return outFilter, nil
}

func (bf *bloomFilter) encode() (out encodedBloomFilter) {
	out.BloomFilterType = byte(multiHashBloomFilter)
	out.EncodingParams = bf.encodingParams
	if bf.filter != nil {
		out.BloomFilter, _ = bf.filter.MarshalBinary()
	}
	return
}
func (bf *bloomFilter) sameParams(other bloomFilter) bool {
	return (bf.encodingParams == other.encodingParams) && (bf.containedTxnsRange == other.containedTxnsRange)
}

func (bf *bloomFilter) test(txID transactions.Txid) bool {
	if bf.filter != nil {
		if bf.encodingParams.Modulator > 1 {
			if txidToUint64(txID)%uint64(bf.encodingParams.Modulator) != uint64(bf.encodingParams.Offset) {
				return false
			}
		}
		return bf.filter.Test(txID[:])
	}
	return false
}

func makeBloomFilter(encodingParams requestParams, txnGroups []transactions.SignedTxGroup, shuffler uint32, hintPrevBloomFilter *bloomFilter) (result bloomFilter) {
	result.encodingParams = encodingParams
	switch {
	case encodingParams.Modulator == 0:
		// we want none.
		return
	case encodingParams.Modulator == 1:
		// we want all.
		if len(txnGroups) > 0 {
			result.containedTxnsRange.firstCounter = txnGroups[0].GroupCounter
			result.containedTxnsRange.lastCounter = txnGroups[len(txnGroups)-1].GroupCounter
			result.containedTxnsRange.transactionsCount = uint64(len(txnGroups))
		}

		if hintPrevBloomFilter != nil {
			if result.sameParams(*hintPrevBloomFilter) {
				return *hintPrevBloomFilter
			}
		}

		sizeBits, numHashes := bloom.Optimal(len(txnGroups), bloomFilterFalsePositiveRate)
		result.filter = bloom.New(sizeBits, numHashes, shuffler)
		for _, group := range txnGroups {
			result.filter.Set(group.FirstTransactionID[:])
		}
	default:
		// we want subset.
		result.containedTxnsRange.firstCounter = math.MaxUint64
		filtedTransactionsIDs := getTxIDSliceBuffer(len(txnGroups))
		defer releaseTxIDSliceBuffer(filtedTransactionsIDs)

		for _, group := range txnGroups {
			txID := group.FirstTransactionID
			if txidToUint64(txID)%uint64(encodingParams.Modulator) != uint64(encodingParams.Offset) {
				continue
			}
			filtedTransactionsIDs = append(filtedTransactionsIDs, txID)
			if result.containedTxnsRange.firstCounter == math.MaxUint64 {
				result.containedTxnsRange.firstCounter = group.GroupCounter
			}
			result.containedTxnsRange.lastCounter = group.GroupCounter
		}

		result.containedTxnsRange.transactionsCount = uint64(len(filtedTransactionsIDs))

		if hintPrevBloomFilter != nil {
			if result.sameParams(*hintPrevBloomFilter) {
				return *hintPrevBloomFilter
			}
		}

		sizeBits, numHashes := bloom.Optimal(len(filtedTransactionsIDs), bloomFilterFalsePositiveRate)
		result.filter = bloom.New(sizeBits, numHashes, shuffler)
		for _, txid := range filtedTransactionsIDs {
			result.filter.Set(txid[:])
		}
	}

	return
}

func txidToUint64(txID transactions.Txid) uint64 {
	return uint64(txID[0]) + (uint64(txID[1]) << 8) + (uint64(txID[2]) << 16) + (uint64(txID[3]) << 24) + (uint64(txID[4]) << 32) + (uint64(txID[5]) << 40) + (uint64(txID[6]) << 48) + (uint64(txID[7]) << 56)
}