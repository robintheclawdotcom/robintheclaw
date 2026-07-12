# Releasing

## Versioning

Semantic versioning. Contracts and the published packages version independently. Breaking changes
to a deployed contract are a major bump and require a migration note.

## Cutting a release

1. Ensure `main` is green (CI, `forge test`, verifier self-test) and the working tree is clean.
2. Update `CHANGELOG.md` with the notable changes.
3. Tag with a signed, annotated tag:

   ```bash
   git tag -s vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

4. For contract releases, record the deployed addresses and the verified source link in the
   release notes, and add the audit reference under `audits/` if one applies.

## Deploys

Contract deploys run through `contracts/script/Deploy.s.sol` against a named network. Record the
resulting addresses in the release notes and in `config/addresses.json`. Never commit a deployer
key; keys are read from a mode-checked file supplied at deploy time.
