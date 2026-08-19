package l4

import (
	"bufio"
	"bytes"
	"testing"
)

// FuzzReadCommand feeds arbitrary bytes as a RESP client command. The array and
// bulk length headers are attacker-controlled; readCommand must bound them
// (maxRESPArgs / maxRESPBulk) and never panic or allocate unboundedly. The
// oversized seeds below regress the DoS fix: a huge "*N"/"$N" is rejected before
// any allocation.
func FuzzReadCommand(f *testing.F) {
	f.Add([]byte("*2\r\n$4\r\nAUTH\r\n$3\r\nfoo\r\n")) // valid AUTH
	f.Add([]byte("*1\r\n$4\r\nPING\r\n"))
	f.Add([]byte("*0\r\n"))                  // n < 1 -> rejected
	f.Add([]byte("*999999999999\r\n"))       // huge array -> bounded, rejected
	f.Add([]byte("*1\r\n$999999999999\r\n")) // huge bulk -> bounded, rejected
	f.Add([]byte("*1\r\n$-5\r\n"))           // negative bulk -> rejected
	f.Add([]byte("garbage"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, b []byte) {
		r := bufio.NewReader(bytes.NewReader(b))
		args, err := readCommand(r) // must not panic or OOM
		if err != nil {
			return
		}
		if len(args) > maxRESPArgs {
			t.Fatalf("readCommand returned %d args, over cap %d", len(args), maxRESPArgs)
		}
		for _, a := range args {
			if len(a) > maxRESPBulk {
				t.Fatalf("readCommand returned a %d-byte arg, over cap %d", len(a), maxRESPBulk)
			}
		}
	})
}
