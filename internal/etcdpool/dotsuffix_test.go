package etcdpool

import "testing"

func TestLongestCommonDotSuffix(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{
			name:  "single name",
			input: []string{"node-aa-1.internal.example"},
			want:  "",
		},
		{
			name:  "empty",
			input: nil,
			want:  "",
		},
		{
			name:  "common domain suffix",
			input: []string{
				"node-aa-1.internal.example",
				"node-bb-1.internal.example",
				"node-cc-1.internal.example",
			},
			want: ".internal.example",
		},
		{
			name:  "partial domain overlap",
			input: []string{
				"node-a.us.example.com",
				"node-b.us.example.com",
				"node-c.eu.example.com",
			},
			want: ".example.com",
		},
		{
			name:  "no common suffix at dot boundary",
			input: []string{
				"alpha",
				"bravo",
				"charlie",
			},
			want: "",
		},
		{
			name:  "identical names",
			input: []string{"etcd-1", "etcd-1"},
			want:  "",
		},
		{
			name:  "common letters but not at dot boundary",
			input: []string{
				"node-aa-1",
				"node-bb-1",
			},
			want: "", // shared "-1" doesn't start at a dot
		},
		{
			name:  "trailing dots collapse",
			input: []string{
				"a.cluster.local",
				"b.cluster.local",
			},
			want: ".cluster.local",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := longestCommonDotSuffix(tc.input)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIndexByteFromStart(t *testing.T) {
	if indexByteFromStart("hello.world", '.') != 5 {
		t.Errorf("dot in middle: %d", indexByteFromStart("hello.world", '.'))
	}
	if indexByteFromStart("no-dots", '.') != -1 {
		t.Errorf("no dots: %d", indexByteFromStart("no-dots", '.'))
	}
	if indexByteFromStart(".leading", '.') != 0 {
		t.Errorf("leading dot: %d", indexByteFromStart(".leading", '.'))
	}
}
