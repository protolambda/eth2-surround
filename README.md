# Surround Vote Matching

| deep dive in surround vote matching problem, by @protolambda.

Attestations can "surround" other attestations in ETH 2.0. And this is a slashable offence.
However, since there can be long delays, and there are many attestations,
 it is hard to match attestations with the complete set of known attestations, to find these surround votes.

And with considerable numbers (8 months of data worth 54.000 epochs, with approx. 300.000 validators), it is important to be space-efficient with the surround vote matching.

## Definition

Surround vote definition:

```
s: source
t: target

a surrounds b if: s_a < s_b < t_b < t_a

s < t is a pre, so the condition can also be:

s_a < s_b and t_b < t_a

```

## Problem

An attestation is really a `(t, s)` point. And finding related points, is a geo-spatial problem.

This point can be visualized:

![](img/surround-problem.png)

for an existing point `x` and a new point `p`:

- *I*: `s_x > s_p` and `t_p < t_x`. A.k.a. `x > p`. So `p` is an old attestation.
- **II**: `s_x < s_p` and `t_p < t_x`. `x` surrounds `p`.
- **III**: `s_x < s_p` and `t_p > t_x`. A.k.a. `p > x`. So `p` is a new attestation.
- **IV**: `s_x > s_p` and `t_p > t_x`. A.k.a. `s_p < s_x` and `t_x < t_p`. `p` surrounds `x`. 

The `t_p = t_x` line are double votes (except where `s_p = s_x`).

The `s_p = s_x` line is free: any target goes if the source is right.

The `s_p = s_x && t_p = t_x` point is the same attestation (if you ignore the crosslink / metadata).

The `s > t` case is simply invalid, attestations are required to have a newer target than source.

Note that the `s = t` line (or `s + 1 = t` really) is the ideal: 
 attestations build right on the last epoch, and everything is justified and finalized quickly.

The father you go from `s = t`, the worse your attestation is.
This also results in higher density of attestations near this line.
But in chaotic times, an attestation may fork off, and stray away. 

![](img/surround-density.png)

And, as you move away from `s = t` with a new attestation, the area IV grows: 
 you can surround your earlier vote if you backtrack to an earlier source while voting for a new target.

## Geo-spatial matching

To make use of the `s > t` space in the matching, the axis can be transformed to move it out of bounds:
 
![](img/surround-skewed.png)

`d` here is some maximum distance between `s` and `t` to keep track of.

This would be something like 2 weeks, in a super exceptional case.
It is also acceptable to set it lower, as the data beyond `d` is rare and has a higher chance of being a surround vote.
So it can be handled as a special case.

### Throw a quadtree at it

Now, to catch the II (surrounded by existing attestation) and IV (surrounds existing attestation) cases, we can apply some spatial search  algorithm, like a quadtree:

**Note: this is an _aggressive_ approach to the problem. If the gradient from target to source is such that there are
 next to none attestations with a source-target distance > ~4 (it should be), then the quadtree is too much.**  

**Also, the space complexity of the quadtree is really bad; finding a surround may be fast, but the storage costs are not worth it**

![](img/surround-quadtree.png)

This quadtree goes in full depth near the edges, but if there are no attestations in a quad, one can stop recursively going deeper.
And when a quad is fully encompassed, the full set of attestations corresponding to the quad can be returned.

A quadtree is square however; it does not go beyond `d` in width, so future attestations would be out of bounds.
This is avoided by defining chunks, each `d * d` in size:

![](img/surround-chunks.png)

And then you binary search the chunks based on target epoch, and select the two chunks to search in
 (for any point `p` in the chunk, the II and IV areas can only overlap one more neighbour chunk)

Then search the quadtree of the chunk (or just a list would work, if the quadtree contents are nearly flat),
 and the surrounded and surrounded-by attestations (may both be none) for a new `p` can be retrieved.

The binary search could be 32 bits deep, and then another 32 levels search in the quadtree. And so for both II and IV.
This would result in approximately `(32 + 32*2) * 2` bound checks when no matching attestation for an attester-slashing can be found. (recursion stops for empty/unrelated quads)

These bit depths may be less, see limits.

The cost of updating the data structure is similar: find the leaf node for the point, and maybe fold it out one level deeper if the node holds too many points.

Note that old chunks are used rarely, and can be persisted to disk. And chaos may be clustered, so caching the older chunks that are hit often helps.

The chunking is probably more useful with other more efficient matching algorithms, see alternatives below.

### Limits

8 months weak subjectivity period with 300K validators: 54000 epochs

2 weeks of exceptional no finality: 3150 epochs

`log2(3150)=11.6` bits, so 12 levels of quadtree if `d = 3150`. Note that the leafs of quadtrees may be optimized to hold multiple values. So a depth of 8 may work.

`54000/d = 54000/3150 = 17.14`, so 18 chunks may be sufficient to cover the full period. And then keep the last few in memory for efficiency.

### Layered approach

How to deal with false-positives? Idea: Use them, design to a layered filtering system.

The above matching can work on a per-validator basis.
But one may change this to a layered approach for storage/memory efficiency.
**This is arguably more important than whatever data structure is used for a chunk, as the average-case distribution within a chunk should be very close to the `s=t` line.
Optimizations to avoid per-validator work help more in such case.**

1. Full approach, but per attestation. May return many false positives. But can rule out many guaranteed safe attestations (a.k.a. no hit in quadtree).
  - **Do not have to recurse all the way down** Knowing that there is at least 1 matching attestation is good enough. Fully encapsulated sub-quads are preferable to check first. If not empty, work is done.
2. If hit, take the validator indices, and reduce their index precision to e.g. `index >> 5`. This would "confuse" 32 validators as the same. Again, false positives. But also short-cut if non is hit.
  - Again, with false positives, we are not interested in the exact attestations, just that they exist. Shortcut where possible.
3. Repeat the index-precision thing a layer or two maybe.
4. Now that we know with high certainty that there is indeed a slashable attestation, with a rough range from the last time time we hit the quadtree. 
   We can stream the hit attestation(s) (and filter the few of them if we stopped at a non-exact precision, e.g. `index >> 3` may turn up attestations for 8 validators).

### Manhatten index

Idea here: Less false positives with design a for specialized index.

We can exploit the gradient between `s = t` and `s + d = t`.

By rotating the plot, a 1D index can be aligned to `s=t`, and make range queries on this index 
for a certain point `p` *effectively* retrieve more attestations in area IV than in III and I.
This is because of the gradient: most attestations are close to the `s = t` line.

A manhatten distance (`distance = m(a, b) = a + b`) can be used for the indexing.

Note that there are still two ends of area I and III inside the `[m(s,s) ... m(t,t)]` range. 
These would be false positives. However, since only a tiny portion is near the `s=t` line (the dense part) there shouldn't be many.

![](img/surround-manhatten.png)

The index can be split into multiple indices, for ranges of distances to the `s=t` line (or source - target distance really), to reduce false positives even more.
And then fully surrounded range queries can be executed also, to get attestation matches for cheap (no false positives, simply stop on first match).
Or query on full range first, to rule out any attestations (no false negatives).

This strategy can be changed depending on the distance to the `s=t` line, as density influences the amount of false-positives.

Along the index you would put a bitfield: set a bit for each `(s,t)` that maps to a given `m(s,t)` on the index.
And to check if anything is in area IV you check if any bits are set in `m(s,s) ... m(t,t)`, and filter out the false positives.
Or for some block that starts at a manhatten distance `e` from the `s=t` line, you check `m(s,s)+e ... m(t,t)-e`.

A similar thing could be done for the surrounded-by (area II) check, but this would be more sparse and not strictly bounded. One of the shortcomings of this approach.

Space: `300000 * (54000 + D) * M`, where `D` is the maximum distance between source and target (e.g. `2*7*24*3600/6/64 = 3150` for 2 weeks of epochs) and `M` is the amount of bitfields per validator, for less false positives.
That would be `300000 * (54000 + 3150) bits = 2.14 GB` for `M=1`, or `21.4 GB` for a more realistic `M = 10`


## Min-Max surround

A newer idea is that you may only care about *any* attestation to slash with, not a specific one. In this case, you would only have to store the best candidate for 2 cases:
 "surrounding" (min span) and "surrounded by" (max span).

Simply read the value at the position equal to the source of the incoming attestation, on the min-span and max-span index to figure out if it is surrounding or gets surrounded by existing attestations.

![](img/surround-min-surrounding.png)


![](img/surround-max-surrounded-by.png)

This would be a `O(1)`, with 54000 epochs of history, it is sufficient to read just 4 bytes to figure out if it can be slashed!
Updating can be more expensive than other solutions like the manhatten index however, but not too much.

Note that instead of distances to target, the target itself could also be encoded.

For each value (can be distance or target), you would need 2 bytes (`log2(54000) = ~ 15.7  -> 16 bits`).

There is a trade-off here:
- for lots of small spans, encoding the distance is better: lots of small values like 1, 2, 3. It compresses well.
- for bigger spans, encoding the absolute target is better: lots of references to the same target, so values are a lot alike. It compresses well.

Since the max-span and min-span obviously tend to different span sizes here, the preferred encoding can be chosen for both separately.

Another optimization would be to have a few "special values" for low spans in the case you encode the absolute target location; e.g. reserve `0xfff0 ... 0xffff` for distances `0...15` (which can be converted to regular target locations) by substracting these from the index of the value that is read (a.k.a. the source of the incoming attestation).
This ensures that for the smaller spans, values are still the same (instead of incrementing) and compress well.

This solution would take `2 indexes * 54000 epochs * 300000 validators * 2 byte ints = 64.8 GB`. However, the lookups have good locality for caching (latest epochs are close together)
 and can be compressed very well (intuition: to store a partition of `64` epochs, you have `2**64` options, which takes 8 bytes to store, much less than the `2 * 64` bytes it is encoded naively in).

So with compression applied, this solution may get in the `< 1 GB` range for 300.000 validators and 54.000 epochs :tada:

### How to fetch the slashable attestation 

You store attestations by `(target epoch, validator index)`, *note that you likely already have this to find double votes*
 
1. derive the `target` of the found slashing match from `target = (lookup index (== incoming source) - distance)` (or target is just encoded in place already).
2. validator index is already known for the incoming attestation you are matching
3. fetch attestation for `(target epoch, validator index)`
    - there should only be one entry for this, as the validator would get slashed for attesting to the same target with different data.
