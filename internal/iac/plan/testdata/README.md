# Plan fixtures

`show -json` of a saved plan, written by `make iac-fixtures` (test/planfixtures) with the releases test/deps pins:

- `terraform/`: terraform 1.17.2
- `tofu/`: tofu 1.13.9

Each applies one configuration and plans another:

- `all.json`: one change of every type, a data source read at apply, and a changed output
- `none.json`: no change
- `outputs.json`: a change to an output alone

Record them again after a release moves, and update the tests that read them if a release plans otherwise.
