package protocol

// CmdRelayCred is a paired phone asking for its relay credential (docs/mobile/03
// §10.6). It is a handled read-tier command, answered only on a phone's own
// connection; the unix socket has no peer and gets an error.
const CmdRelayCred = "relayCred"

// RelayCredArgs is Request.Args for cmd=relayCred.
type RelayCredArgs struct {
	// Holds is the hex SHA-256 of the bearer the phone holds; "" = none.
	Holds string `json:"holds,omitempty"`
}

// RelayCredData is Response.Data for cmd=relayCred. Bearer is set only when the
// phone must store a new one; it is a secret and travels only inside the inner TLS.
type RelayCredData struct {
	Available bool   `json:"available"`        // relay is eligible on this daemon now
	URL       string `json:"url,omitempty"`    // hub base URL
	RV        string `json:"rv,omitempty"`     // this device's rendezvous id
	Bearer    string `json:"bearer,omitempty"` // set only when a new bearer must be stored
}
