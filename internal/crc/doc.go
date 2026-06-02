// Package crc provides the checksums FLAC requires: CRC-8 over each frame
// header, CRC-16 over each complete frame, and the MD5 of the unencoded audio
// stored in STREAMINFO. Implementations are added in M2.
package crc
