package container

import "time"

// SetWaitWarn changes how long a caller waiting for a build waits before
// logging a warning. It is for tests only: this file does not enter a
// production build, and because the threshold is kept per container it does not
// affect concurrent tests.
func SetWaitWarn(c *Container, d time.Duration) {
	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()
	c.reg.waitWarn = d
}
