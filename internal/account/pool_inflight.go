package account

// 在途请求（账号正在生成回复）的只读视图。
//
// 账号从 Acquire/AcquireWait 拿到名额到请求结束 Release 期间一直计在 inUse 里，
// 这正是"正在生成回复"的账号。弹性号池重算与代理桥自动调度都必须在改配置前
// 读它：把在途账号禁用/换绑会让 mihomo 全量重启并掐断流式连接，用户侧看到的
// 就是回复被中途打断。

// InUseIDs 返回当前仍有在途请求的账号标识集合快照（无在途账号时为 nil）。
// 调用方应在进入 Store.Update 事务之前取快照：Pool.Status 会持 p.mu 再读 Store，
// 在事务内取快照会与它构成锁序反转。
func (p *Pool) InUseIDs() map[string]bool {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.inUse) == 0 {
		return nil
	}
	out := make(map[string]bool, len(p.inUse))
	for id, count := range p.inUse {
		if count > 0 {
			out[id] = true
		}
	}
	return out
}

// HasInflight 报告是否仍有账号在途（正在生成回复）。
func (p *Pool) HasInflight() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, count := range p.inUse {
		if count > 0 {
			return true
		}
	}
	return false
}
