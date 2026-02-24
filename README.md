# Dojo operator

Kubernetes operator for [Dojo](https://github.com/DanijelRadakovic/dojo) application.


## Scaffolding the project from scratch

Commands that are used to scaffold the project:

```bash
kubebuilder init --domain jutsu.com --project-name dojo-operator --repo github.com/DanijelRadakovic/dojo-operator
```

```bash
kubebuilder create api --group core --version v1 --kind Dojo --controller --resource --plural dojos
```

