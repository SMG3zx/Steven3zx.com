package catalogtest

import "testing"

func TestMemoryPassesTheCatalogContract(t *testing.T) {
	Run(t, func(*testing.T) Fixture {
		m := NewMemory()
		return Fixture{
			Catalog: m,
			Connect: m.Connect,
			Publish: func(_ *testing.T, repository string, files map[string]string) string {
				return m.Publish(repository, files)
			},
			Wait: func(*testing.T, string) {},
		}
	})
}
