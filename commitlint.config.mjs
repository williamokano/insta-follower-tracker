export default {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'body-max-line-length': [0, 'always'],
    'footer-max-line-length': [0, 'always'],
    'scope-enum': [
      2,
      'always',
      ['api', 'config', 'docker', 'instagram', 'store', 'tracker', 'ui', 'deps', 'release', ''],
    ],
  },
};
