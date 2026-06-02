// Package bitio implements MSB-first bit-level reading and writing in the bit
// order used by the FLAC bitstream.
//
// It is the foundation for every codec package: metadata parsing, frame and
// subframe coding, and Rice residual coding all read and write through a
// bitio Reader or Writer. The concrete reader and writer are added in M2; this
// file documents the package boundary for the groundwork skeleton.
package bitio
