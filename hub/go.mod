module github.com/sushidev-team/lola/hub

go 1.25.0

require github.com/sushidev-team/lola/hubproto v0.0.0

// hubproto is the wire contract, a sibling module in this repository. The hub
// always builds against the checkout's copy: there is no version to pin.
replace github.com/sushidev-team/lola/hubproto => ../hubproto
