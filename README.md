# Samo Server

A unified self-hosted listening server for music, audiobooks, podcasts, and radio.

Samo Server is not a Navidrome wrapper.
Samo Server is not an Audiobookshelf wrapper.
Samo Server is a native media server built around unified listening history, playback state, devices, queues, and cross-media browsing.

## Initial scope

V0 focuses on:

- running as a small Ubuntu-friendly Go server
- SQLite-backed metadata storage
- adding local library folders
- scanning music files
- exposing a Samo-native API
- streaming original audio files
- accepting playback/scrobble events
- powering Samo-native recents

## Philosophy

- no jank
- no fake data
- no throwaway glue-server architecture
- small boring reliable pieces
- client talks to Samo-native concepts, not backend-specific hacks
