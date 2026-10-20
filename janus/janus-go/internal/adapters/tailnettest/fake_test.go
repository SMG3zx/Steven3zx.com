package tailnettest

import "testing"

func TestFakePassesTheTailnetContract(t *testing.T) {
	Run(t, func(t *testing.T) Subject {
		fake := NewFake()
		return Subject{Tailnet: fake, Join: fake.JoinForContract}
	})
}
