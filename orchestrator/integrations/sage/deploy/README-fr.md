# Fichiers de déploiement préparés

[English version](README.md)

Ces fichiers ne sont que des exemples. Ce dépôt ne les applique jamais.

Avant de les utiliser, confirmez le namespace actif, les noms du Deployment et
du conteneur, le compte de service, le digest de l’image, le commit Waggle, la
version de k3s et la licence amont d’`edge-scheduler`.

Le patch ne redéfinit volontairement pas les variables d’environnement
RabbitMQ. Vérifiez que le Deployment existant conserve ses variables
`RABBITMQ_URI`, `RABBITMQ_USERNAME` et `RABBITMQ_PASSWORD` alimentées par un
Secret ; ne placez pas d’identifiants dans cette ConfigMap et ne comptez pas
sur les valeurs de compatibilité héritées de Waggle.

N’exposez pas et n’appelez pas `POST /api/v1/schedule` depuis le serveur d’API
Waggle épinglé. Ce chemin amont n’initialise pas complètement un
`PluginRuntime` et peut entrer en concurrence avec l’itération de la file.
Conservez ou ajoutez des contrôles réseau autour du port 8080 jusqu’à ce que la
route soit désactivée ou corrigée en amont.

La politique est chargée une seule fois au démarrage de `sage-nodescheduler`.
La mise à jour du volume ConfigMap ne suffit pas à la recharger. Dans le vrai
chart ou la vraie Kustomization, générez un nom de ConfigMap incluant un hash
du contenu ou injectez sa somme de contrôle dans l’annotation du Pod template
`scheduling.sagecontinuum.org/config-revision`.

Pour un pilote manuel contrôlé, un opérateur doit redémarrer délibérément le
Deployment confirmé après avoir modifié la ConfigMap :

```sh
kubectl rollout restart deployment/wes-plugin-scheduler
kubectl rollout status deployment/wes-plugin-scheduler
```

N’exécutez pas ces commandes tant que la cible réelle du déploiement WES n’a
pas été confirmée avec l’équipe SAGE.
