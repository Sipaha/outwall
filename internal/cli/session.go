package cli

import "strings"

// promptPasswordFn is the (stubbable) TTY password prompt used by the sudo-style session helper.
// It defaults to the real terminal prompt; tests swap it to avoid needing a TTY.
var promptPasswordFn = promptPassword

// isSessionRequired reports whether err is the daemon's operator-session gate response. client.Do
// surfaces the daemon's {"error":"operator session required"} body as "daemon: operator session
// required", so a substring match is the stable contract.
func isSessionRequired(err error) bool {
	return err != nil && strings.Contains(err.Error(), "operator session required")
}

// doPrivileged runs a privileged admin call and transparently opens an operator session when the
// daemon reports one is required (sudo-style). On the gate error it prompts for the master password
// on the TTY, POSTs /operator/session/open, and retries the call exactly once. A call that succeeds
// (an already-open session within the idle TTL) never prompts.
func doPrivileged(gf *globalFlags, method, path string, body, out any) error {
	c := newClient(gf)
	err := c.Do(method, path, body, out)
	if err == nil || !isSessionRequired(err) {
		return err
	}
	pw, perr := promptPasswordFn("Operator master password: ")
	if perr != nil {
		return perr
	}
	if oerr := c.Do("POST", "/operator/session/open", map[string]string{"password": pw}, nil); oerr != nil {
		return oerr
	}
	return c.Do(method, path, body, out)
}
