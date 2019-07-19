# Geo-spatial Surround Vote Matching

| deep dive in surround vote matching as a geo-spatial problem, by @protolambda.

Attestations can "surround" other attestations in ETH 2.0. And this is a slashable offence.
However, since there can be long delays, and there are many attestations,
 it is hard to match attestations with the complete set of known attestations, to find these surround votes.

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

- I: `s_x > s_p` and `t_p < t_x`. A.k.a. `x > p`. So `p` is an old attestation.
- II: `s_x < s_p` and `t_p < t_x`. `x` surrounds `p`.
- III: `s_x < s_p` and `t_p > t_x`. A.k.a. `p > x`. So `p` is a new attestation.
- IV: `s_x > s_p` and `t_p > t_x`. A.k.a. `s_p < s_x` and `t_x < t_p`. `p` surrounds `x`. 

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

## Matching

To make use of the `s > t` space in the matching, the axis can be transformed to move it out of bounds:
 
![](img/surround-skewed.png)

`d` here is some maximum distance between `s` and `t` to keep track of.

This would be something like 2 weeks, in a super exceptional case.
It is also acceptable to set it lower, as the data beyond `d` is rare and has a higher chance of being a surround vote.
So it can be handled as a special case.

Now, to catch the II (surrounded by existing attestation) and IV (surrounds existing attestation) cases, we can apply some spatial search  algorithm, like a quadtree:

**Note: this is an _aggressive_ approach to the problem. If the gradient from target to source is such that there are
 next to none attestations with a source-target distance > ~4 (it should be), then the quadtree is too much.**  

![](img/surround-quadtree.png)

This quadtree goes in full depth near the edges, but if there are no attestations in a quad, one can stop recursively going deeper.
And when a quad is fully encompassed, the full set of attestations corresponding to the quad can be returned.

A quadtree is square however; it does not go beyond `d` in width, so future attestations would be out of bounds.
This is easily avoided by defining chunks, each `d * d` in size:

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

## Limits

8 months weak subjectivity period with 300K validators: 54000 epochs

2 weeks of exceptional no finality: 3150 epochs

`log2(3150)=11.6` bits, so 12 levels of quadtree if `d = 3150`. Note that the leafs of quadtrees may be optimized to hold multiple values. So a depth of 8 may work.

`54000/d = 54000/3150 = 17.14`, so 18 chunks may be sufficient to cover the full period. And then keep the last few in memory for efficiency.

## Layered approach

The above can work on a per-validator basis.
But one may change this to a layered approach for storage/memory efficiency:

1. Full approach, but per attestation. May return many false positives. But can rule out many guaranteed safe attestations (a.k.a. no hit in quadtree).
  - **Do not have to recurse all the way down** Knowing that there is at least 1 matching attestation is good enough. Fully encapsulated sub-quads are preferable to check first. If not empty, work is done.
2. If hit, take the validator indices, and reduce their index precision to e.g. `index >> 5`. This would "confuse" 32 validators as the same. Again, false positives. But also short-cut if non is hit.
  - Again, with false positives, we are not interested in the exact attestations, just that they exist. Shortcut where possible.
3. Repeat the index-precision thing a layer or two maybe.
4. Now that we know with high certainty that there is indeed a slashable attestation, with a rough range from the last time time we hit the quadtree. 
   We can stream the hit attestation(s) (and filter the few of them if we stopped at a non-exact precision, e.g. `index >> 3` may turn up attestations for 8 validators).
