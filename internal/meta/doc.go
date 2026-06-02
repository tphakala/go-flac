// Package meta implements FLAC metadata blocks: STREAMINFO, VORBIS_COMMENT,
// SEEKTABLE, PICTURE, CUESHEET, PADDING, and APPLICATION. It parses the seek
// table but owns no seek logic; seek policy lives in the pcm package.
// Implementations are added in M2.
package meta
