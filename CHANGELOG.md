# Changelog

## Next version

* Feature: Monitor all remotes of local repositories instead of just 'origin' ([issue #3](https://github.com/nbedos/cistern/issues/3))
* Bugfix: Lookup path if BROWSER is not a path itself ([issue #8](https://github.com/nbedos/cistern/issues/8))
* Bugfix: Add support for 'insteadOf' and 'pushInsteadOf' configuration for remote URLs  ([issue #7](https://github.com/nbedos/cistern/issues/7))
* Bugfix: Stages of Azure pipelines are now ordered
* Bugfix: Leave URL encoding to go-gitlab ([issue #16](https://github.com/nbedos/cistern/issues/16))
* Chore: Rewrite build script in go for improved maintainability
* Chore: Rename repository


## Version 0.1.2 (2019-12-20)

* Fix: Binary releases now contain an executable built for the right system ([issue #4](https://github.com/nbedos/cistern/issues/4))
* Fix: Appveyor pipelines triggered for a tag incorrectly showed a branch as reference instead of a tag


## Version 0.1.1 (2019-12-18)

* Support for private GitLab instances ([issue #2](https://github.com/nbedos/cistern/issues/2))


## Version 0.1.0 (2019-12-18)
Initial release!
