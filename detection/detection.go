package detection

import "log"

type Source uint64
type Target uint64

type Delta uint64
type MHIndex uint64

func Manhatten(s Source, t Target) MHIndex {
	return MHIndex(s) + MHIndex(t)
}

func ManhattenRange(s Source, t Target) (start, end MHIndex) {
	return MHIndex(s)*2, MHIndex(t)*2
}

func ManhattenSurroundeeRange(s Source, t Target, d Delta) (start MHIndex, end MHIndex) {
	// TODO boundaries? also make the start exclusive; s_x < s_p and t_p < t_x. x surrounds p
	s -= 1
	if maxDelta := CalcDelta(s, t); maxDelta > d {
		d = maxDelta
	}
	return MHIndex(s)*2 + MHIndex(d), MHIndex(t)*2 - MHIndex(d)
}

func ManhattenSurrounderRange(s Source, t Target, d Delta) (start MHIndex, end MHIndex) {
	if minDelta := CalcDelta(s, t); minDelta < d {
		d = minDelta
	}
	if Target(d) > t {
		d = Delta(t)*2
	}
	return MHIndex(t)*2 - MHIndex(d), MHIndex(s)*2 + MHIndex(d)
}

func CalcDelta(s Source, t Target) Delta {
	if Target(s) >= t {
		panic("invalid source target pair!")
	}
	return Delta(t) - Delta(s)
}

type MHBitfield []byte

func NewMHBitfield(maxTarget Target) MHBitfield {
	// max index == manhatten(target, target)
	return make(MHBitfield, ((maxTarget*2)+7)/8)
}

func (b MHBitfield) Check(i MHIndex) bool {
	if i >= MHIndex(len(b)*8) {
		panic("out of range lookup")
	}
	return (b[i>>3] & (1 << (i & 7))) != 0
}

func (b MHBitfield) Set(i MHIndex) {
	if i >= MHIndex(len(b)*8) {
		panic("out of range set")
	}
	b[i>>3] |= 1 << (i & 7)
}

type Root [32]byte
type ValidatorIndex uint64

type AttestationData struct {
	source Source
	target Target
	block  Root
}

type ValidatorIndexSet []ValidatorIndex

func (vis ValidatorIndexSet) QueryRange(start ValidatorIndex, end ValidatorIndex) (out ValidatorIndexSet) {
	for _, vi := range vis {
		if vi >= start && vi < end {
			out = append(out, vi)
		}
	}
	return
}

type Focus struct {
	// the validators that could be surrounding their previous attestations
	surrounding ValidatorIndexSet
	// the validators that could be surrounded by their previous attestations
	surroundedBy ValidatorIndexSet
	// the validators that could be double voting
	double ValidatorIndexSet
}

func NewFocus(validators []ValidatorIndex) *Focus {
	return &Focus{
		surrounding:  validators,
		surroundedBy: validators,
		double:       validators,
	}
}

func (f *Focus) IsEmpty() bool {
	return f == nil || (len(f.surrounding) == 0 && len(f.surroundedBy) == 0 && len(f.double) == 0)
}

func (f *Focus) QueryRange(start ValidatorIndex, end ValidatorIndex) *Focus {
	return &Focus{
		surrounding:  f.surrounding.QueryRange(start, end),
		surroundedBy: f.surroundedBy.QueryRange(start, end),
		double:       f.double.QueryRange(start, end),
	}
}

type SlashType byte

const (
	// surrounding: the attestations the incoming attestation surrounds
	Surrounding SlashType = iota
	// surroundedBy: the attestations the incoming attestation is surrounded by
	SurroundedBy
	// double: any double votes
	Double
)

type Slash struct {
	index ValidatorIndex
	root Root
	slashType SlashType
}

type FuzzyDetector interface {
	FuzzyCheckAndAdd(source Source, target Target, focus *Focus) (out *Focus)
}

type Detector interface {
	// CheckAndAdd checks if there is anything to slash (added to slashings result), and adds the attestation to the detector memory
	CheckAndAdd(att *AttestationData, focus *Focus) (slashings []Slash)

	// A real-world detection service should have a Save() and Load()
}

type GroupedDetectionLayer struct {
	GroupSize  ValidatorIndex
	GateKeeper FuzzyDetector
	Groups     []Detector
}

type DetectorFactory func() Detector

func NewGroupedCheck(size ValidatorIndex, total ValidatorIndex, gate FuzzyDetector, next DetectorFactory) *GroupedDetectionLayer {
	if size == 0 {
		panic("group size cannot be 0")
	}
	count := uint64((total + size - 1) / size)
	res := &GroupedDetectionLayer{
		GroupSize: size,
		GateKeeper: gate,
		Groups:    make([]Detector, count, count),
	}
	for i := uint64(0); i < count; i++ {
		res.Groups[i] = next()
	}
	return res
}

func (gd *GroupedDetectionLayer) CheckAndAdd(att *AttestationData, focus *Focus) (out []Slash) {
	// filter more
	focus = gd.GateKeeper.FuzzyCheckAndAdd(att.source, att.target, focus)
	if focus.IsEmpty() {
		return
	}
	start := ValidatorIndex(0)
	end := gd.GroupSize
	for _, g := range gd.Groups {
		subFocus := focus.QueryRange(start, end)
		if subFocus.IsEmpty() {
			continue
		}
		out = append(out, g.CheckAndAdd(att, subFocus)...)
		start = end
		end = start + gd.GroupSize
	}
	return
}

type MHDetectionBlock struct {
	MinDistance Delta
	MaxDistance Delta
	bits MHBitfield
}

func (db *MHDetectionBlock) Applicable(source Source, target Target) bool {
	d := CalcDelta(source, target)
	return d >= db.MinDistance && d < db.MaxDistance
}

func (db *MHDetectionBlock) Add(source Source, target Target) {
	if !db.Applicable(source, target) {
		panic("invalid attestation")
	}
	m := Manhatten(source, target)
	db.bits.Set(m)
}

func (db *MHDetectionBlock) CheckRange(start, end MHIndex) bool {
	for i := start; i < end; i++ {
		if db.bits.Check(i) {
			return true
		}
	}
	return false
}

type MHDetectionBlockStack struct {
	blocks []MHDetectionBlock
}

func NewMHDetectionBlockStack(gradient []Delta, maxTarget Target) {
	bs := &MHDetectionBlockStack{}
	min := Delta(0)
	for _, d := range gradient {
		bs.blocks = append(bs.blocks, MHDetectionBlock{
			MinDistance: min,
			MaxDistance: min + d,
			bits:        NewMHBitfield(maxTarget),
		})
		min = min + d
	}
}

func (bs *MHDetectionBlockStack) FuzzyCheckAndAdd(source Source, target Target, focus *Focus) (out *Focus) {
	d := CalcDelta(source, target)
	// TODO: anything that can be done for double votes?
	out = &Focus{double: focus.double}
	for i := range bs.blocks {
		b := &bs.blocks[i]
		// check the range, but only the relevant part.
		// [m(s,s), m(s,s+d_min)] and [m(t-d_min,t),m(t,t)] only have false positives.
		// [m(s,s+d_min), m(s,s+d_max)] and [m(t-d_min,t),m(t-d_max,t)] can have both true/false positives
		// [m(s,s+d_max), m(t-d_max,t)] only has true positives

		if d >= b.MinDistance && d < b.MaxDistance {
			b.Add(source, target)
		}
		// if the block ends past the new data point, then the data point can be surrounded by something in it
		if d < b.MaxDistance {
			start, end := ManhattenSurrounderRange(source, target, b.MaxDistance)
			if b.CheckRange(start, end) {
				out.surroundedBy = focus.surroundedBy
				break
			}
		}
		// if the block starts before the new data point, then the data point can be surrounding something in the block
		if d > b.MinDistance {
			start, end := ManhattenSurroundeeRange(source, target, b.MinDistance)
			if b.CheckRange(start, end) {
				out.surrounding = focus.surrounding
				break
			}
		}
	}
	log.Println("Did not hit any of the blocks, distance is too large. Cannot filter focus more.")
	return out
}

