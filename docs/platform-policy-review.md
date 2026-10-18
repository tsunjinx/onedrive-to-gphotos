# Platform policy review

Reviewed 2026-10-17 for a personal, owner-authorized OneDrive -> Google Photos migration. No documented use-case prohibition was identified. This is a design review, not platform approval or a guarantee about future behavior; account configuration and implemented handling still need verification.

## Microsoft

Microsoft API terms section 3(b)(3) permits migration away when the customer approves it. Your request supplies that intent; browser consent will authorize actual account access. Register the app accurately, use minimum permissions, protect credentials and only copy data needed for this transfer. Do not bypass service limits. Source access will be read-only. [Microsoft API terms](https://learn.microsoft.com/en-us/legal/microsoft-apis/terms-of-use).

Honor throttling responses and Retry-After, reducing concurrency rather than rotating registrations to evade limits. Test scaling with fixtures rather than load-testing Microsoft's service. [Graph throttling](https://learn.microsoft.com/en-us/graph/throttling).

## Google

The Photos policy permits personal image/video storage integrations that add functionality. This importer fits that described use case; it is not a general gallery, CDN or enterprise-data store. Before OAuth, disclose the transfer direction, requested scopes, local storage and data use, and require affirmative consent. Provide a privacy notice and instructions for deleting local app data. Encrypt tokens and cached personal data at rest; no advertising, resale or unrelated data use. [Photos policy](https://developers.google.com/photos/support/api-policy).

Show the connected destination account and provide an obvious disconnect command. Use the full Google Photos product name in user-facing text, plain text branding and no suggestion of endorsement. [UX requirements](https://developers.google.com/photos/support/ux-guidelines).

## Gates before live transfer

- Explain access and local data retention in the normal login flow; provide a privacy notice before consent.
- Validate granted scopes/account identity. Store tokens securely. Add logout/disconnection and local cleanup documentation before calling the authentication milestone complete.
- Implement provider rate limits, storage failure handling and video-readiness checks.
- Verify the owner's OAuth consent configuration; personal use does not automatically waive applicable policies or verification requirements.
- Current bootstrap downloads only the official Go toolchain and builds local code. It does not sign in, access media or send data to Microsoft/Google Photos. The download server receives the normal network request for the Go archive.
