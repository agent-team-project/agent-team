# Topology Template Fragments

`template/instances.toml.tmpl` is generated from the ordered fragments in
`instances.toml.tmpl.d/`.

Edit the focused fragment for the topology area you are changing, then run:

```sh
python3 scripts/ci/generate_instances_template.py
```

CI runs the same script with `--check`. Both bundled template profiles exclude
`template/topology/`, so these source fragments do not get rendered into a
consumer repo's `.agent_team/` tree.
