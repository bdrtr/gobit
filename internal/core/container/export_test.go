package container

import "time"

// SetWaitWarn kurulumu bekleyen çağıranın uyarı loglamadan önce bekleyeceği
// süreyi değiştirir. Yalnızca testler içindir: bu dosya üretim derlemesine
// girmez ve eşik container başına tutulduğu için eşzamanlı testleri etkilemez.
func SetWaitWarn(c *Container, d time.Duration) {
	c.reg.mu.Lock()
	defer c.reg.mu.Unlock()
	c.reg.waitWarn = d
}
