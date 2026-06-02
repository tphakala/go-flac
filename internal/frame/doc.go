// Package frame implements FLAC frame and subframe coding: the frame header
// (with CRC-8 sync validation and frame-sync scanning used for seek and
// resync), the four subframe types (constant, verbatim, fixed, LPC), and
// inter-channel decorrelation (left/side, right/side, mid/side). Decode lands
// in M2, encode in M3.
package frame
