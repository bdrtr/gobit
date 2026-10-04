package identitypasskey

import "time"

// SetNoticeTimeout shortens a module's bound on a notice, for a test that
// waits it out.
func SetNoticeTimeout(m *Module, d time.Duration) { m.noticeTimeout = d }
