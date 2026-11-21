const random = require('@pulumi/random');
const value = new random.RandomString('example', {length: 12, special: false});
exports.value = value.result;
exports.proof = 'runner-image-contract';
