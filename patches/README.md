# Patches

## korecube-web-monitoring-rbac-storage.patch

Apply the Vue frontend updates locally:

```bash
git clone https://github.com/korecube/korecube-web.git
cd korecube-web
git checkout -b cursor/sync-monitoring-rbac-storage-b44a
git apply ../korecube/patches/korecube-web-monitoring-rbac-storage.patch
# or: git am < ../korecube/patches/korecube-web-monitoring-rbac-storage.patch
git push -u origin HEAD
```

Contents: monitoring page, StorageClass, K8s RBAC views, clusterId injection, locales.
