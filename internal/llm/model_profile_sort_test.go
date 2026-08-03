package llm

import (
	"testing"
)

// TestProfileOrderingIsDeterministic 锁定 ModelRegistry 各查询方法的输出顺序稳定性。
//
// 背景：这些方法都从 r.profiles（map）遍历收集结果，而 Go 的 map 迭代顺序是
// 随机的。历史上排序只以 Tier 为键、缺少 tie-breaker，导致同层级模型之间的
// 相对顺序每次调用都可能不同 —— 表现为 Router 候选池顺序抖动、前端模型列表
// 闪烁，以及 TestAvailableProfiles 随机失败。
//
// 本测试对同一个 registry 重复调用多次并比对结果，一旦有人再引入无次级键的
// 排序就会失败。
func TestProfileOrderingIsDeterministic(t *testing.T) {
	reg := NewModelRegistry()

	// 刻意注册多个「同层级」模型：只有存在 Tier 相同的项，缺少 tie-breaker
	// 的排序才会暴露出不确定性。
	for _, p := range []*ModelProfile{
		{Name: "zeta/model-a", Provider: "zeta", Tier: TierStandard, Source: SourceConfiguredProvider},
		{Name: "alpha/model-b", Provider: "alpha", Tier: TierStandard, Source: SourceConfiguredProvider},
		{Name: "mid/model-c", Provider: "mid", Tier: TierStandard, Source: SourceConfiguredProvider},
		{Name: "beta/model-d", Provider: "beta", Tier: TierEfficient, Source: SourceConfiguredProvider},
		{Name: "gamma/model-e", Provider: "gamma", Tier: TierEfficient, Source: SourceConfiguredProvider},
	} {
		reg.Register(p)
	}

	cases := []struct {
		name string
		call func() []*ModelProfile
	}{
		{"List", reg.List},
		{"AvailableProfiles", func() []*ModelProfile {
			return reg.AvailableProfiles(map[string]bool{}, true)
		}},
		{"FilterByContextLen", func() []*ModelProfile {
			return reg.FilterByContextLen(0)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := profileNames(tc.call())
			if len(want) == 0 {
				t.Fatalf("%s 返回空集合，测试前提不成立", tc.name)
			}
			// map 迭代顺序每次都会重新随机，多轮比对即可捕获不稳定排序。
			for i := 0; i < 50; i++ {
				got := profileNames(tc.call())
				if len(got) != len(want) {
					t.Fatalf("第 %d 轮长度不一致：want %v, got %v", i, want, got)
				}
				for j := range got {
					if got[j] != want[j] {
						t.Fatalf("第 %d 轮顺序不稳定：want %v, got %v", i, want, got)
					}
				}
			}
			// 同层级必须按 Name 升序，避免退化成「碰巧稳定」的实现。
			for i := 1; i < len(want); i++ {
				prev, cur := tc.call()[i-1], tc.call()[i]
				if prev.Tier == cur.Tier && prev.Name > cur.Name {
					t.Fatalf("%s 同层级未按 Name 升序：%q 排在 %q 之前", tc.name, prev.Name, cur.Name)
				}
			}
		})
	}
}
