# Match sign-in identities by provider ID, never by email

Google and GitHub identities are matched by the provider's stable ID, not by email. A verified email that already belongs to an Account doesn't merge the two; the user must sign in to that Account and connect the method from there. We chose this to avoid accidental Account merges, at the cost of a manual step for users who sign in with a second provider.
