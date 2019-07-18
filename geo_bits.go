package eth2_surround

type GeoIndex uint64

type Epoch uint64

type Attestation struct {
	Source     Epoch
	Target     Epoch
	StoragePtr uint64 // some other data, POC.
	// TODO: per-validator checks.
}

func NewGeoIndex(source Epoch, target Epoch, offset Epoch) GeoIndex {
	if (source-offset) >= EPOCH_DISTANCE || (target-offset) >= EPOCH_DISTANCE {
		panic("insufficient offset to handle geo index within range")
	}
	return GeoIndex(InterleaveUint32(uint32(source-offset), uint32(target-offset))) >> ZOOM
}

// both EPOCH_DISTANCE and EPOCHS_PER_REFRESH constants must be a multiple of 8.
// maximum bitfield epochs
const EPOCH_DISTANCE = 54000
// some range to keep going, and then refresh (adjust offset) the bitfield later.
const EPOCHS_PER_REFRESH = 1000

const TOTAL_EPOCHS = EPOCH_DISTANCE + EPOCHS_PER_REFRESH

// log2(TOTAL_EPOCHS) = log2(54000 + 1000) = 15.74...
const EPOCH_BITS = 16
const GEO_INDEX_BITS = EPOCH_BITS * 2

// reduce it by precision bits:
// index = some GEO_INDEX_BITS bits number
// adjusted_index = index >> ZOOM
// ZOOM = 0: perfect precision
// ZOOM = 10: less precision, 2**5= 32 epochs collision
const ZOOM = 10

const BIT_DEPTH = GEO_INDEX_BITS - ZOOM

// 1 bit per geo index
// structured in orders of 2. first slot unused. Then 1 for root, 2 for root childs, 4 for root child childs, etc.
type GeoBitfield [1 << BIT_DEPTH << 1]byte

type GeoLookup struct {
	bits GeoBitfield
	offset Epoch
}

func (gl *GeoLookup) MoveOffset(newOffset Epoch) {
	if newOffset < gl.offset || newOffset >= gl.offset + EPOCHS_PER_REFRESH {
		panic("cannot move to offset, out of range data")
	}
	if newOffset % 8 != 0 {
		panic("for efficiency reasons, only offset in multiples of 8 are allowed")
	}
	gl.offset = newOffset
	// move back the bytes
	// TODO error prone, check off by 1
	diff := int(((newOffset - gl.offset) << EPOCH_BITS) >> ZOOM) / 8
	copy(gl.bits[:], gl.bits[:diff])
	// zero the freed bytes at the end
	copy(gl.bits[len(gl.bits) - diff:], make([]byte, diff, diff))
}

func Bitmapping(index GeoIndex, depth uint8) (byteIndex uint64, bitIndex uint64) {
	level := uint64(1) << depth
	levelIndex := uint64(index) >> (BIT_DEPTH - depth)
	bitfieldIndex := level | levelIndex
	byteIndex = bitfieldIndex >> 3
	bitIndex = bitfieldIndex & 0x7
	return
}

// TODO: abstract this away from a bitfield. Could work for "normal" geospatial datasets too.
func (gl *GeoLookup) Hit(index GeoIndex, depth uint8) bool {
	byteIndex, bitIndex := Bitmapping(index, depth)
	return ((gl.bits[byteIndex] >> bitIndex) & 1) == 1
}

// adds the attestation to the bitfield
func (gl *GeoLookup) AddAttestation(at *Attestation) {
	index := NewGeoIndex(at.Source, at.Target, gl.offset)
	// set a bit on each zoom level, to mark the level as "contains attestation"
	for i := uint8(0); i < BIT_DEPTH; i++ {
		byteIndex, bitIndex := Bitmapping(index, i)
		gl.bits[byteIndex] |= 1 << bitIndex
	}
	// TODO adjust offset if necessary.
}

func (gl *GeoLookup) MatchAttestation(at *Attestation) (surrounds []GeoIndex, surroundedBy []GeoIndex) {
	index := NewGeoIndex(at.Source, at.Target, gl.offset)
	// TODO: use map[geo index]*Attestation to return the attestations themselves instead. And filter out false positive if ZOOM != 0.
	return gl.match(index, index, 0, true)
}

const MAX_MASK = (GeoIndex(1) << BIT_DEPTH) - 1

// TODO: need to check the flip + quadrant math (off by 1 prone)

// recursively collect attestations
func (gl *GeoLookup) match(index GeoIndex, current GeoIndex, depth uint8, flip bool) (surrounds []GeoIndex, surroundedBy []GeoIndex) {
	a := current &^ (MAX_MASK >> depth)
	b := a ^ (1 << (BIT_DEPTH - depth))
	next := depth + 1
	// add the results of a deeper match to the total results
	// TODO: use a channel instead to stream results back
	recurse := func(into GeoIndex) {
		s1, s2 := gl.match(index, into, next, !flip)
		if s1 != nil {
			surrounds = append(surrounds, s1...)
		}
		if s2 != nil {
			surroundedBy = append(surroundedBy, s2...)
		}
	}
	// TODO fix quadrants recursive behavior, this is the idea, but probably wrong now
	if (flip != (a < index)) && gl.Hit(a, depth) {
		if next == BIT_DEPTH && a != index {
			surrounds = append(surrounds, a)
		} else {
			recurse(a)
		}
	}
	if (flip != (a > index)) && gl.Hit(a, depth) {
		if next == BIT_DEPTH && a != index {
			surroundedBy = append(surroundedBy, b)
		} else {
			recurse(a)
		}
	}
	if (flip != (b < index)) && gl.Hit(b, depth) {
		if next == BIT_DEPTH && b != index {
			surrounds = append(surrounds, b)
		} else {
			recurse(b)
		}
	}
	if (flip != (b > index)) && gl.Hit(b, depth) {
		if next == BIT_DEPTH && b != index  {
			surroundedBy = append(surroundedBy, b)
		} else {
			recurse(b)
		}
	}
	return
}
