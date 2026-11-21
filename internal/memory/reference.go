package memory

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

func makeReference(scope, root, memoryRoot, fileName string, line int, version [sha256.Size]byte) string {
	digest := sha256.New()
	digest.Write([]byte("smem-record-reference-v1"))
	writeReferenceField(digest, []byte(scope))
	writeReferenceField(digest, []byte(root))
	writeReferenceField(digest, []byte(memoryRoot))
	writeReferenceField(digest, []byte(fileName))
	var lineBytes [8]byte
	binary.BigEndian.PutUint64(lineBytes[:], uint64(line))
	writeReferenceField(digest, lineBytes[:])
	writeReferenceField(digest, version[:])

	return "r1-" + hex.EncodeToString(digest.Sum(nil)[:16])
}

func fileVersion(data []byte) [sha256.Size]byte {
	return sha256.Sum256(data)
}

func writeReferenceField(digest hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	digest.Write(length[:])
	digest.Write(value)
}

func validReference(ref string) bool {
	if len(ref) != 35 || ref[:3] != "r1-" {
		return false
	}
	for i := 3; i < len(ref); i++ {
		char := ref[i]
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}
