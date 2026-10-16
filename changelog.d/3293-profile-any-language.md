### Fixed
- **A new metadata profile with no language ticked now allows any language** (#3293). The form sent an empty list and the server turned it into English only on create, so the profile card said English even though you had cleared it. Editing a profile was never affected.
