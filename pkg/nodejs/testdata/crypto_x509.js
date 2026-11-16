// Runs unchanged in Node and in Orbital. crypto_x509.node.json is this script's output under
// Node v20.18.1. Regenerate it with
//   ORBITAL_CAPTURE_NODE=1 go test ./pkg/nodejs -run 'TestCryptoX509_MatchesNode'
// The certificates were made with Go's crypto/x509 for this test: a CA, a leaf it issued
// with most extensions, a self-signed P-384 certificate with an unusual subject, an
// Ed25519 certificate with alt names that need quoting, a SAN-only certificate with an
// empty subject, a leaf whose issuer is the CA's name in other case and spacing, and an
// Ed25519 certificate with the CA's name and key identifier.
const certs = {
 "ca": "-----BEGIN CERTIFICATE-----\nMIID6DCCAtCgAwIBAgICAQIwDQYJKoZIhvcNAQELBQAwgZsxCzAJBgNVBAYTAlVT\nMREwDwYDVQQIEwhOZXcgWW9yazERMA8GA1UEBxMIQnJvb2tseW4xEzARBgNVBAoT\nCkV4YW1wbGUgQ28xGDAKBgNVBAsTA0VuZzAKBgNVBAsTA09wczEYMBYGA1UEAxMP\nRXhhbXBsZSBSb290IENBMR0wGwYJKoZIhvcNAQkBDA5jYUBleGFtcGxlLmNvbTAg\nFw0yNDAxMDUwMzA0MDVaGA8yMDYwMDEwNTAzMDQwNVowgZsxCzAJBgNVBAYTAlVT\nMREwDwYDVQQIEwhOZXcgWW9yazERMA8GA1UEBxMIQnJvb2tseW4xEzARBgNVBAoT\nCkV4YW1wbGUgQ28xGDAKBgNVBAsTA0VuZzAKBgNVBAsTA09wczEYMBYGA1UEAxMP\nRXhhbXBsZSBSb290IENBMR0wGwYJKoZIhvcNAQkBDA5jYUBleGFtcGxlLmNvbTCC\nASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALajF5thG4kgOXJeespVulxg\nsfHbg/voIiXkXBEHfyn4tOGLEoe3Uppz9kiuhy2NmV7dSK4EfCqUbwSnYnsxt+MI\nRLen28rkRkQcefeSico8fcpCUy2DWj6Y9BtTsdqNppaqoUOD8beh2vbVCTSerfgh\nak0/6xWyo2/agLhlCEAdnA5RVc+V7khGojYg2rc4+vL+0iZMX3zzIfli5Cdpr8ET\nnSRfQiY38e+KRN65wrL+CZ0DcRRb8Z5LjWv3491oSsIZcks5fWbmlS0XCITzzJT7\nMXEDWG6lUbvEIThthXKCBlykF7pRK6rO4w7L83cdlgmZWidkvhDedOSs2OLdEU0C\nAwEAAaMyMDAwDgYDVR0PAQH/BAQDAgEGMA8GA1UdEwEB/wQFMAMBAf8wDQYDVR0O\nBAYEBAECAwQwDQYJKoZIhvcNAQELBQADggEBAAWoobx1rse2XTFiQXPEuJ9m0ceb\nGRhI5r07hndCdolgMaJWBPKGjNd7FE0Om41hvol0/tfrGIHSi5gyjeJN0aS0l8Sg\nAn0B63d72L2a0RWVpLg75iPDF0HYwgKH/hOy9N4HmTPfoYOeAoaX9ulUZABwOzlF\nSAsEmcOZjRSjioSmpE2iaj9vPx/yIT/odJcgmgG2Gq1GBs7XUsVGM85XzNjwpnfK\n7z5pSwTtvLYIXxgpdtCaC7VVqjQy0dUdVq4ZZ3KQb1x1ucH1fm6MF8h225QiPtl0\nJKUrbfNt9sU6xoOdLFg3augA4goub5DZ4URBgiLRgbZqm459xUUXUD0BGP4=\n-----END CERTIFICATE-----\n",
 "caKey": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC2oxebYRuJIDly\nXnrKVbpcYLHx24P76CIl5FwRB38p+LThixKHt1Kac/ZIroctjZle3UiuBHwqlG8E\np2J7MbfjCES3p9vK5EZEHHn3konKPH3KQlMtg1o+mPQbU7HajaaWqqFDg/G3odr2\n1Qk0nq34IWpNP+sVsqNv2oC4ZQhAHZwOUVXPle5IRqI2INq3OPry/tImTF988yH5\nYuQnaa/BE50kX0ImN/HvikTeucKy/gmdA3EUW/GeS41r9+PdaErCGXJLOX1m5pUt\nFwiE88yU+zFxA1hupVG7xCE4bYVyggZcpBe6USuqzuMOy/N3HZYJmVonZL4Q3nTk\nrNji3RFNAgMBAAECggEAKsLSPOS2VfuHv72bucJrJQRVvWcURpdh4WXPYYRUE29h\n6FnPr5tuOW/zpq5IQ5Pe6C1evjGaIb0JpjkLj4UDRSJzNl3UkfMy+XECZIL4hHN3\nzzKKt4IEz9mvc7N7lYxrR+Tw53Yd0s0QT5Du/BU37bvjH+2afMTLy91InIO5NAtk\nv03opOR4/wkdZPtJaLQqplW1jSpRIn9csenwxLaksDgGXFbsVeoMliKW9/hOe9OS\nEsTVNHjDEW4eS84Q63xmfskRDWPgm5x9J2ml8/DZV5Yjl5lBYP3li+/T/iIP44Vx\nzRKxIcGg3l/W5lhiWbomS3tqVrmbxECCLNtxAVDnAQKBgQDzXmHhGTj5U/EIcdMr\nkh/VN5DMvClNvLBL0nCa2zlsBUKlijIQgsge2ser1fqzFgNAC9t43agL0ilVX+W0\nGAHfEyS+FbviscyfYva+7FhSF/xEqxDQHCkQkaT4tadjcBdMCAHEGhWwrZWGCXle\nkAR6NfAQtNe9zB5xDqIoTnadAQKBgQDAHcZKIEyIlbgGCNiA2g2orLrDpSQvsoOC\nCltFJRu3Sr0KkMy4oju7zd0aNzOubH/2jaZiCsiCgT/mVvM689i2kMDHRVh3XgGe\nRKNsl9Bh1buh0DCJwuAMQ1hyhDudjjm9KZG3/f/Ipu6fstZiZip2sEi0DwD2Nee+\nk14F+bfYTQKBgB53FjGjgqpTg7zW4uR1al66kfnPJCyDDl6vfUElaE7JacpnPlUq\nyfpy0MQGCVgZzVZbJaI/vSWdrWbulkaxKqrOLq9M8TQzpbXeIZ1M4NGGByJhd71V\niR513Pz1YHrmMURnTDFMQqLXV/xrezIHPUg/MEonGUq9HqJwfoQlLlwBAoGBAI3Q\n7OEnOpfQvYlm2jszu8V5zFxD+ZRkGIPUFU/TdsQ1HZtBDH5289VMwBxsB3CIk2Vz\nZxEUmLMNqhfks6QfASizbjsrS4TCFju4u1KcNq0h02I9hrcK7czfodjXGBLUPHcQ\nyoqhq0F5SMkGgQ8eD9aRhvmz4afeXvXlXxDWKAo5AoGAaoAJgDf0hTWVHKgwaQ3L\nmjwCnP2vBeTm7s/2lr2n2Qit4t5k52OZVvHdDHubr7uEGiiQyl78XaG55rUT01qt\nghFEYisRdCYywOrNsXlGZhw8VTZ+mlpFSYdQKIF16Zo5rpkRrS9T+lmXCJx+ONnN\n8z/sHlArTj8GusKZZWoKWuU=\n-----END PRIVATE KEY-----\n",
 "ed": "-----BEGIN CERTIFICATE-----\nMIIBRzCB+qADAgECAgEAMAUGAytlcDAXMRUwEwYDVQQDEwxlZDI1NTE5IHRlc3Qw\nHhcNMjQwMTA1MDMwNDA1WhcNMjQwMjA0MDMwNDA1WjAXMRUwEwYDVQQDEwxlZDI1\nNTE5IHRlc3QwKjAFBgMrZXADIQB3na5fxg8NenDpK87zbnR3asS0JmLpdHWwVkfM\nBua91KNrMGkwDgYDVR0PAQH/BAQDAgeAMA8GA1UdEwEB/wQFMAMBAf8wHQYDVR0O\nBBYEFNTwaOesFCI1qtO7+ZogFEKh9aBcMCcGA1UdEQQgMB6CC2EsYi5leGFtcGxl\ngg9xdW90ZSJkLmV4YW1wbGUwBQYDK2VwA0EAC4lyag1XKUTbQFVuU6g33INiceGC\n9HUh4pLZQPNGFw7bWdOHwIfHCV398zOnUfsMlEyHCimoGxusm+G/CxVBBQ==\n-----END CERTIFICATE-----\n",
 "edSameName": "-----BEGIN CERTIFICATE-----\nMIIB2TCCAYugAwIBAgIBDTAFBgMrZXAwfDELMAkGA1UEBhMCVVMxETAPBgNVBAgT\nCE5ldyBZb3JrMREwDwYDVQQHEwhCcm9va2x5bjETMBEGA1UEChMKRXhhbXBsZSBD\nbzEYMAoGA1UECxMDRW5nMAoGA1UECxMDT3BzMRgwFgYDVQQDEw9FeGFtcGxlIFJv\nb3QgQ0EwHhcNMjQwMTA1MDMwNDA1WhcNMjQwMTA2MDMwNDA1WjB8MQswCQYDVQQG\nEwJVUzERMA8GA1UECBMITmV3IFlvcmsxETAPBgNVBAcTCEJyb29rbHluMRMwEQYD\nVQQKEwpFeGFtcGxlIENvMRgwCgYDVQQLEwNFbmcwCgYDVQQLEwNPcHMxGDAWBgNV\nBAMTD0V4YW1wbGUgUm9vdCBDQTAqMAUGAytlcAMhAA8ngUOB0/rrIDGpigMYU42/\nHQr4JqCiSaRKqU4Xq5lZozIwMDAOBgNVHQ8BAf8EBAMCAgQwDwYDVR0TAQH/BAUw\nAwEB/zANBgNVHQ4EBgQEAQIDBDAFBgMrZXADQQCngOXjpVngzsf+KHmviN8XOw5o\nbm6uejaDUingtbnCv/k+QlYa2VFWev5az+N67H1qayVSEKWPb2u7to1XxkwI\n-----END CERTIFICATE-----\n",
 "emptySubject": "-----BEGIN CERTIFICATE-----\nMIICfzCCAWegAwIBAgIBCzANBgkqhkiG9w0BAQsFADCBmzELMAkGA1UEBhMCVVMx\nETAPBgNVBAgTCE5ldyBZb3JrMREwDwYDVQQHEwhCcm9va2x5bjETMBEGA1UEChMK\nRXhhbXBsZSBDbzEYMAoGA1UECxMDRW5nMAoGA1UECxMDT3BzMRgwFgYDVQQDEw9F\neGFtcGxlIFJvb3QgQ0ExHTAbBgkqhkiG9w0BCQEMDmNhQGV4YW1wbGUuY29tMB4X\nDTI0MDEwNTAzMDQwNVoXDTI0MDEwNjAzMDQwNVowADBZMBMGByqGSM49AgEGCCqG\nSM49AwEHA0IABAtCOFIvgXTBsttj+L5PLWmnEwW/YOwWAlkzYEw+ty3o9KEEwqS2\n1MRB0kqg3dQrZ6DjWxcjqO5jYvMYW1bAl4qjMzAxMA8GA1UdIwQIMAaABAECAwQw\nHgYDVR0RAQH/BBQwEoIQb25seS5leGFtcGxlLmNvbTANBgkqhkiG9w0BAQsFAAOC\nAQEANOVvD3O91ZUNaUTRSxuUrFutXxzAEzJjalZvtnMWIBoPizN5GgZO+v++uUKZ\n6sL0lErr2BDv29TGwcYgvCaS3qHgOnWAcu/2lFILlEi2gF0IS0GeM0ZO/BKM9byd\nt31LxaOexi44Ky/GRn+YM/dmrjScmgUl4r+IIwEIoc01qChB28ZMTPZEsaySN5JZ\nc5c3v9C468AWT1YZbQQJeDj/myTVUX0++Ve7Yq3dsEuoOdncwiJVwaCIJNJNutaN\nFDlCLEtRA2Em4ig6me9gM2Sf7yNGfDTpXlrG22Pap3riJMs3P4eCBTZY1ze/Tx1n\nb7TU5rE9V4WsbNBwImNimAFL7A==\n-----END CERTIFICATE-----\n",
 "leaf": "-----BEGIN CERTIFICATE-----\nMIIDzDCCArSgAwIBAgIFAI8BAgMwDQYJKoZIhvcNAQELBQAwgZsxCzAJBgNVBAYT\nAlVTMREwDwYDVQQIEwhOZXcgWW9yazERMA8GA1UEBxMIQnJvb2tseW4xEzARBgNV\nBAoTCkV4YW1wbGUgQ28xGDAKBgNVBAsTA0VuZzAKBgNVBAsTA09wczEYMBYGA1UE\nAxMPRXhhbXBsZSBSb290IENBMR0wGwYJKoZIhvcNAQkBDA5jYUBleGFtcGxlLmNv\nbTAeFw0yNDAxMDUwMzA0MDVaFw0yNDAyMDkxMzE0MTVaMEMxCzAJBgNVBAYTAlVT\nMRowGAYDVQQKDBFFeGFtcGxlLCAiQ28iIDx4PjEYMBYGA1UEAxMPd3d3LmV4YW1w\nbGUuY29tMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEAsaN1nBWupWaeAkf1tnA\n+HnDZN8hfJr+7vML764CrpEP62kLqluSfDbWx1aIi+MePL9fIR+Gnk/rK1xbkE+f\nD6OCATcwggEzMA4GA1UdDwEB/wQEAwIHgDAdBgNVHSUEFjAUBggrBgEFBQcDAQYI\nKwYBBQUHAwIwDwYDVR0jBAgwBoAEAQIDBDBdBggrBgEFBQcBAQRRME8wIwYIKwYB\nBQUHMAGGF2h0dHA6Ly9vY3NwLmV4YW1wbGUuY29tMCgGCCsGAQUFBzAChhxodHRw\nOi8vY2EuZXhhbXBsZS5jb20vY2EuY3J0MIGRBgNVHREEgYkwgYaCD3d3dy5leGFt\ncGxlLmNvbYINKi5leGFtcGxlLm9yZ4IQZm9vKi5leGFtcGxlLm5ldIERYWRtaW5A\nZXhhbXBsZS5jb22HBH8AAAGHEAAAAAAAAAAAAAAAAAAAAAGHECABDbgAAAAAAAAA\nAAAAAAWGFWh0dHBzOi8vZXhhbXBsZS5jb20veDANBgkqhkiG9w0BAQsFAAOCAQEA\nXt/4VE/ToMhxlS0I06onlzbdoGwYTNuT9VAbcWTCZiX5HPd0/I4XmObatKOZUuqO\n8WVkcQ+7KB/lBCOJbLxle3C64jJtVWe/4RXtNnGUWTESrMTEQUizvwzjmuqhspxu\n9QyFL6MTH0ewFMSvNIE0gwhV9zB0PoNwhu0D3b6y2vHWCtdno6U78IKuU7qHznq3\nzNUadNL8ubv4PGPiu3fW1r8oekbez/7g0jvCH9roseCeyeG+TrYc+MJMlvPuivlA\nwfahmojepm3DVLwUFMFk0Z7KpUpIiiOEZwx5fNeWcXrSiK0j+UKD7HESP9HROkrc\nyyZJz8XDQ4FNF3V285Qqng==\n-----END CERTIFICATE-----\n",
 "leafKey": "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgp0R+YYZsT/yjrHsR\nmWIE5HAA6+4h4MOFk6R+JugWnKmhRANCAAQCxo3WcFa6lZp4CR/W2cD4ecNk3yF8\nmv7u8wvvrgKukQ/raQuqW5J8NtbHVoiL4x48v18hH4aeT+srXFuQT58P\n-----END PRIVATE KEY-----\n",
 "looseIssuer": "-----BEGIN CERTIFICATE-----\nMIICczCCAVugAwIBAgIBDDANBgkqhkiG9w0BAQsFADCBnzELMAkGA1UEBhMCdXMx\nEjAQBgNVBAgTCW5ldyAgeW9yazESMBAGA1UEBxMJIEJyb29rbHluMRMwEQYDVQQK\nEwpFWEFNUExFIENPMRgwCgYDVQQLEwNlbmcwCgYDVQQLEwNvcHMxGjAYBgNVBAMT\nEWV4YW1wbGUgICByb290IGNhMR0wGwYJKoZIhvcNAQkBDA5DQUBleGFtcGxlLmNv\nbTAeFw0yNDAxMDUwMzA0MDVaFw0yNDAxMDYwMzA0MDVaMBAxDjAMBgNVBAMTBWxv\nb3NlMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEC0I4Ui+BdMGy22P4vk8taacT\nBb9g7BYCWTNgTD63Lej0oQTCpLbUxEHSSqDd1CtnoONbFyOo7mNi8xhbVsCXiqMT\nMBEwDwYDVR0jBAgwBoAEAQIDBDANBgkqhkiG9w0BAQsFAAOCAQEAQB2y5aJcXHI/\nn7CaVl1nOup3loPSUs6ujftuOyoRIGwsJfQy48p9Mb4k6WpzDWOz9UOAKCzHPj4i\nNhWKUQDoiJKfbxmBRU3du9evSVKYYLj7a5wwYEfBTRQab2YsrDt79Sjb+9udlFIl\nbcWbecAoW+yjye/nK6VbQ9E/DmuYO+ZMm4NP9xlQHO8c7HnXCOf4kpNPlky5aEh+\nURLuxaq6Y0LQ7iRNhDuKdTzrtKu235CHogP1BgR1sPcdh/9oWP9IpsKSoQjV/id1\nC4rz5mAzc6pL1FxFB9jtjPXYSiCdOJCv0z4KuY9Isy5/VAKQFz6/Y7raZKTsDz2q\n9/5zXIh6kQ==\n-----END CERTIFICATE-----\n",
 "odd": "-----BEGIN CERTIFICATE-----\nMIICGDCCAZ6gAwIBAgIBBzAKBggqhkjOPQQDAzB2MRkwFwYDVQQDExBob3N0LmV4\nYW1wbGUuY29tMR4wHAYDVQQKDBVaw7xyaWNoICsgU8O2aG5lOyAjMSAxFzAVBgoJ\nkiaJk/IsZAEZEwdleGFtcGxlMQ4wDAYDVQQFEwVTTi00MjEQMA4GBCoDBAUTBmN1\nc3RvbTAgFw0yNDAxMDUwMzA0MDVaGA8yMDUxMDYwNzA4MDkxMFowdjEZMBcGA1UE\nAxMQaG9zdC5leGFtcGxlLmNvbTEeMBwGA1UECgwVWsO8cmljaCArIFPDtmhuZTsg\nIzEgMRcwFQYKCZImiZPyLGQBGRMHZXhhbXBsZTEOMAwGA1UEBRMFU04tNDIxEDAO\nBgQqAwQFEwZjdXN0b20wdjAQBgcqhkjOPQIBBgUrgQQAIgNiAATun9R0t4J6J0ds\ngZ0M/KDffMOBmfsGwgI1ASAOkl9PZnEHQoF9PShMNmCrWBklD+CdYufHUX/7gZMG\nOQH6BC2zFCVnTnwib5xu4JBeb9ZtKq+81l2tjwEq/3ZW9PolDr8wCgYIKoZIzj0E\nAwMDaAAwZQIxAMNdLbi/wr2m03VS3tqmBOPnMJcGCsfpNm+rLSrxcgVGy6Av0hZg\nuEAMoZJigCtI/wIwJf3bcdRdg9aWmgq5DaLLYeGGz3H34r4pGPT218f57F6MqlSU\nVaal4nKUpLJqExwg\n-----END CERTIFICATE-----\n"
};

const crypto = require('crypto');
const { X509Certificate } = crypto;
const results = {};

function show(value) {
  if (Buffer.isBuffer(value)) return 'Buffer ' + value.toString('hex');
  if (value === undefined) return 'undefined';
  if (value instanceof crypto.KeyObject) return 'KeyObject ' + value.type + ' ' + value.asymmetricKeyType;
  return value;
}

function outcome(f) {
  try {
    return { value: show(f()) };
  } catch (e) {
    return { name: e.name, code: e.code, message: e.message };
  }
}

const parsed = {};
for (const name of ['ca', 'leaf', 'odd', 'ed', 'emptySubject']) {
  const c = parsed[name] = new X509Certificate(certs[name]);
  const r = {};
  for (const k of ['ca', 'fingerprint', 'fingerprint256', 'fingerprint512', 'issuer', 'infoAccess', 'keyUsage',
    'serialNumber', 'subject', 'subjectAltName', 'validFrom', 'validTo', 'issuerCertificate', 'publicKey', 'raw']) {
    r[k] = outcome(() => c[k]);
  }
  r.toString = c.toString() === certs[name];
  r.toJSON = c.toJSON() === certs[name];
  r.legacy = outcome(() => {
    const l = c.toLegacyObject();
    const o = {};
    for (const [k, v] of Object.entries(l)) o[k] = show(v);
    return o;
  });
  results[name] = r;
}

const { ca, leaf, odd, ed } = parsed;
// The CA as OpenSSL writes it with trust settings (openssl x509 -addtrust serverAuth -trustout).
const trustedCA = "-----BEGIN TRUSTED CERTIFICATE-----\nMIID6DCCAtCgAwIBAgICAQIwDQYJKoZIhvcNAQELBQAwgZsxCzAJBgNVBAYTAlVT\nMREwDwYDVQQIEwhOZXcgWW9yazERMA8GA1UEBxMIQnJvb2tseW4xEzARBgNVBAoT\nCkV4YW1wbGUgQ28xGDAKBgNVBAsTA0VuZzAKBgNVBAsTA09wczEYMBYGA1UEAxMP\nRXhhbXBsZSBSb290IENBMR0wGwYJKoZIhvcNAQkBDA5jYUBleGFtcGxlLmNvbTAg\nFw0yNDAxMDUwMzA0MDVaGA8yMDYwMDEwNTAzMDQwNVowgZsxCzAJBgNVBAYTAlVT\nMREwDwYDVQQIEwhOZXcgWW9yazERMA8GA1UEBxMIQnJvb2tseW4xEzARBgNVBAoT\nCkV4YW1wbGUgQ28xGDAKBgNVBAsTA0VuZzAKBgNVBAsTA09wczEYMBYGA1UEAxMP\nRXhhbXBsZSBSb290IENBMR0wGwYJKoZIhvcNAQkBDA5jYUBleGFtcGxlLmNvbTCC\nASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALajF5thG4kgOXJeespVulxg\nsfHbg/voIiXkXBEHfyn4tOGLEoe3Uppz9kiuhy2NmV7dSK4EfCqUbwSnYnsxt+MI\nRLen28rkRkQcefeSico8fcpCUy2DWj6Y9BtTsdqNppaqoUOD8beh2vbVCTSerfgh\nak0/6xWyo2/agLhlCEAdnA5RVc+V7khGojYg2rc4+vL+0iZMX3zzIfli5Cdpr8ET\nnSRfQiY38e+KRN65wrL+CZ0DcRRb8Z5LjWv3491oSsIZcks5fWbmlS0XCITzzJT7\nMXEDWG6lUbvEIThthXKCBlykF7pRK6rO4w7L83cdlgmZWidkvhDedOSs2OLdEU0C\nAwEAAaMyMDAwDgYDVR0PAQH/BAQDAgEGMA8GA1UdEwEB/wQFMAMBAf8wDQYDVR0O\nBAYEBAECAwQwDQYJKoZIhvcNAQELBQADggEBAAWoobx1rse2XTFiQXPEuJ9m0ceb\nGRhI5r07hndCdolgMaJWBPKGjNd7FE0Om41hvol0/tfrGIHSi5gyjeJN0aS0l8Sg\nAn0B63d72L2a0RWVpLg75iPDF0HYwgKH/hOy9N4HmTPfoYOeAoaX9ulUZABwOzlF\nSAsEmcOZjRSjioSmpE2iaj9vPx/yIT/odJcgmgG2Gq1GBs7XUsVGM85XzNjwpnfK\n7z5pSwTtvLYIXxgpdtCaC7VVqjQy0dUdVq4ZZ3KQb1x1ucH1fm6MF8h225QiPtl0\nJKUrbfNt9sU6xoOdLFg3augA4goub5DZ4URBgiLRgbZqm459xUUXUD0BGP4wDDAK\nBggrBgEFBQcDAQ==\n-----END TRUSTED CERTIFICATE-----\n";
results.trusted = outcome(() => {
  const t = new X509Certificate(trustedCA);
  return [t.fingerprint256 === ca.fingerprint256, t.toString().split('\n')[0], t.subject === ca.subject];
});
// OtherName alt names (UPN, XmppAddr, SRVName and one Node doesn't print), and an issuer of
// two RDNs (CN=a, 1.2.3.4=b) next to a CA whose one CN is "a\x011.2.3.4=b".
const more = {
 "oneCN": "-----BEGIN CERTIFICATE-----\nMIIBTDCB86ADAgECAgEWMAoGCCqGSM49BAMCMBYxFDASBgNVBAMMC2EBMS4yLjMu\nND1iMB4XDTI0MDEwNTAzMDQwNVoXDTI0MDEwNTA0MDQwNVowFjEUMBIGA1UEAwwL\nYQExLjIuMy40PWIwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAAQHBuL4BF0M6q0h\n9//gxfktwxAQWpGImHj3dD7u2uiuNWlLPeM+r9mZQ3PYxBCNum7kI1/4sTy5DKqo\nwka8WyG0ozIwMDAPBgNVHRMBAf8EBTADAQH/MB0GA1UdDgQWBBRfiszfHjKBT7ko\nz9sXYl2D366y8DAKBggqhkjOPQQDAgNIADBFAiAwPNjHFnbA5Vy9FNSdC6l/oXiP\nA6FXPFn37QG94yHrPgIhAPmESJgSQLTMY8gvZ4AtOid6VIl5I9h37HfdYZru3QJX\n-----END CERTIFICATE-----\n",
 "otherName": "-----BEGIN CERTIFICATE-----\nMIIBrTCCAVOgAwIBAgIBFTAKBggqhkjOPQQDAjAUMRIwEAYDVQQDEwlvdGhlcm5h\nbWUwHhcNMjQwMTA1MDMwNDA1WhcNMjQwMTA1MDQwNDA1WjAUMRIwEAYDVQQDEwlv\ndGhlcm5hbWUwWTATBgcqhkjOPQIBBggqhkjOPQMBBwNCAAQHBuL4BF0M6q0h9//g\nxfktwxAQWpGImHj3dD7u2uiuNWlLPeM+r9mZQ3PYxBCNum7kI1/4sTy5DKqowka8\nWyG0o4GVMIGSMIGPBgNVHREEgYcwgYSgIAYKKwYBBAGCNxQCA6ASDBB1c2VyQGV4\nYW1wbGUuY29toB4GCCsGAQUFBwgFoBIMEHhtcHBAZXhhbXBsZS5jb22gHwYIKwYB\nBQUHCAegExYRX2xkYXAuZXhhbXBsZS5jb22gDgYDKgMEoAcMBW90aGVygg9kbnMu\nZXhhbXBsZS5jb20wCgYIKoZIzj0EAwIDSAAwRQIhAK7/HE8Agapk1QrR0K9mjXKu\nkIyi3TrDRdlAoJfSkzwsAiBjgGkeaVEMOJb8b6WYvKx9RD/ggf8XH1fOBjgjTbat\nIA==\n-----END CERTIFICATE-----\n",
 "twoRDNIssuer": "-----BEGIN CERTIFICATE-----\nMIIBEzCBuqADAgECAgEXMAoGCCqGSM49BAMCMBgxCjAIBgNVBAMTAWExCjAIBgMq\nAwQTAWIwHhcNMjQwMTA1MDMwNDA1WhcNMjQwMTA1MDQwNDA1WjAPMQ0wCwYDVQQD\nEwRsZWFmMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEBwbi+ARdDOqtIff/4MX5\nLcMQEFqRiJh493Q+7trorjVpSz3jPq/ZmUNz2MQQjbpu5CNf+LE8uQyqqMJGvFsh\ntDAKBggqhkjOPQQDAgNIADBFAiEApW3/pUh7lQ8ea8FKMHmKdHmPZG6PjityMx5g\nRRTCuPcCIFKhacf+6cb8gA7WgChg0ZT83MzlZ5zIe53gqKqHwJZD\n-----END CERTIFICATE-----\n",
  "hashCA": "-----BEGIN CERTIFICATE-----\nMIIBkDCCATagAwIBAgIURrMb581lv67aoZ0NxiLw//xjWhAwCgYIKoZIzj0EAwIw\nFzEVMBMGA1UEAwwMIyBFWEFNUExFIENBMB4XDTI2MTExNjAyNTY1OVoXDTM2MTEx\nMzAyNTY1OVowFzEVMBMGA1UEAwwMIyBFWEFNUExFIENBMFkwEwYHKoZIzj0CAQYI\nKoZIzj0DAQcDQgAEYDQarMF/k4Nitdlmbg1aDkYZbFgKaEp1T+3VTr7Cdx5SW8Ye\nTcr6/WvUV1cPWZe54InGCW0VFTmECyT/X+YbiqNgMF4wHQYDVR0OBBYEFC9VYY86\nq9/tsCo/8iY1eAkFdb6SMB8GA1UdIwQYMBaAFC9VYY86q9/tsCo/8iY1eAkFdb6S\nMA8GA1UdEwEB/wQFMAMBAf8wCwYDVR0PBAQDAgIEMAoGCCqGSM49BAMCA0gAMEUC\nICLpeGAG7RsCn/viPR17pelp0c79FIlaQPcbj3b64Xu0AiEA6qcfTA+ODcuoSXDx\nKUiQvo9pCgkt0orbDbslwDkrXaY=\n-----END CERTIFICATE-----\n",
  "hashLeaf": "-----BEGIN CERTIFICATE-----\nMIIBDTCBtAIBCDAKBggqhkjOPQQDAjAXMRUwEwYDVQQDDAwjIGV4YW1wbGUgY2Ew\nHhcNMjYxMTE2MDI1NzA3WhcNMzYxMTEzMDI1NzA3WjAPMQ0wCwYDVQQDDARsZWFm\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAETvURM0DnoDETwzGrq6Mr0xLsadDL\nOT/DsKe7/gF/9nYwsJq3xr25ylJ3l2tqY04Go525cqYU91xWj6dJ+HtCyTAKBggq\nhkjOPQQDAgNIADBFAiEArn9FPnx6YqNbWurGYnIXsA595m96gBqmBlnvXY6ccJwC\nIBRio5ynagMTsLabhek4FUK7lrQEfIo5BtmcgxBgUOug\n-----END CERTIFICATE-----\n",
  "rsaSha224": "-----BEGIN CERTIFICATE-----\nMIIDAzCCAeugAwIBAgIUKpuWJoip/mPnmFlcMtME21SlWDQwDQYJKoZIhvcNAQEO\nBQAwETEPMA0GA1UEAwwGc2hhMjI0MB4XDTI2MTExNjA3NDc0N1oXDTM2MTExMzA3\nNDc0N1owETEPMA0GA1UEAwwGc2hhMjI0MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A\nMIIBCgKCAQEAjVEhj9XIEw+fMG3VhVe7qek1Q5u0mWKz5hXVLy+Jl1OvPvSOP0B9\nGF5WJhf6L1//zLoPY5VkoDfzA5j4EMdR7uWnpScuQoNh1a27Bk07cvAErSM9W9dR\n+8Zq+MZLl0TMAd/HMhSSsz82bnJdHTfXGDoKq1Jtxk8OWvvrKzAxOkzngUzw/PQV\noL/EHzBKtTNQmitCMWd/gEapayLkz7r3rApX5d057wS4geDfHZ9IHLwIt+w6hzWi\nIH9jK1/xnKkzT4dwa5J2rijLUN8wbIn3CLIOJalToFm19oDFICFKIxx3dqYJNA2f\nSKw6qhPJ1BYfE8PGMxNnuzyUcmgPnow7CQIDAQABo1MwUTAdBgNVHQ4EFgQUZk5A\nW2Gyt4WcQKTqsqTWWyM4DN4wHwYDVR0jBBgwFoAUZk5AW2Gyt4WcQKTqsqTWWyM4\nDN4wDwYDVR0TAQH/BAUwAwEB/zANBgkqhkiG9w0BAQ4FAAOCAQEAIokpz++6YWnk\naSmZgF9yC42ulDQknULG9sxkXKDc5+PkGnaHhg1XbszT8GIAwTZIXaZhwyXjIdNQ\n4FJmtUfpj4ybqDS1uvG3r011o9KKUMvc38g8l4MzbWJD3bbEd+JA2aFBecKG2nKB\nZ4A92ucS8WQr2DPKLQ9jRHFFP6chyhGHFH7I+PDzQf+jdb4fG/enI//xWyuNj1Wm\nUg+qHuva6WPl9aBjvYcMe9PEKI3Qk11EaN4coYUPz3MeDxbixUz/VDAKghZ88lw0\nMPF2tVDbi2BZELzK+7c6j4pRQpkNdQZIQ0byvF6JhV0O29ul1/lRyPSO5zkpvZKR\nWBbuHstHnA==\n-----END CERTIFICATE-----\n",
  "rsaSha224Key": "-----BEGIN PRIVATE KEY-----\nMIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQCNUSGP1cgTD58w\nbdWFV7up6TVDm7SZYrPmFdUvL4mXU68+9I4/QH0YXlYmF/ovX//Mug9jlWSgN/MD\nmPgQx1Hu5aelJy5Cg2HVrbsGTTty8AStIz1b11H7xmr4xkuXRMwB38cyFJKzPzZu\ncl0dN9cYOgqrUm3GTw5a++srMDE6TOeBTPD89BWgv8QfMEq1M1CaK0IxZ3+ARqlr\nIuTPuvesClfl3TnvBLiB4N8dn0gcvAi37DqHNaIgf2MrX/GcqTNPh3BrknauKMtQ\n3zBsifcIsg4lqVOgWbX2gMUgIUojHHd2pgk0DZ9IrDqqE8nUFh8Tw8YzE2e7PJRy\naA+ejDsJAgMBAAECggEABPVq9bC5EKcojTLbbk5/6qRA3taSuZufp6QOOxXCUJWq\nhW1gJOrKwO1FfVhzylAGai0Ar3w9X8xayf5m/PK7hqQXE/naGxIgX5a1JHFXE89F\ngLxG8ccuTKT/matemWgb9vhe1VxoseK20p0PdKAiG2rk9zJBIRdiz/E9RSKPpaWi\nExZ5BQzyr7h9TweMfWPhvXiO/ko2BOHvp69RKicW7opkwNA9xkhR3Aa4RNzIovIo\nDDIOCEWkQJPir1ZUBdi3moWtUh1QeJhqLiSzXhSSGkw+pTIyp5m6Xoh1dhoGnSfO\nheYTz4FkyEMQtY7LDWH4QmlFkHWr6CS0HJUC1r/bAQKBgQDDWWicQHUJ8JBnrRsQ\nbIZlRPA0h+vlQqLaAbG1aSma7/cNsueya3O87fJp62/E6J/pBe48OPCsPPb9ZnIj\nBnSvr525y/E9Efp9VWKmnVUTebJBxvLFcApx7qAcIy/lJof8YP0AFO9qiUo4FJ+5\nxLxHqMZDwCJWkTLduouelHWXAQKBgQC5MSsM8dLGicXoWRtct5hEsxWDiqbNkF8U\nCuskTLLjhI8MOZx5uKD7wxFWpIy3pxLJj+NjyBQddBVp9fcMZJywkoJTgAp4jyCK\nHi071qTttB1Ki/SQk6Zy06UMK294lkiRGoT8T2xSiW+tmmu+N+XQTnltmx3jo9cd\n+tJjvDXsCQKBgQCn9Mhfh3/ZYu8VZJ0fWt+WVFZeI/rq64cO/2PmQJuX7few3IAf\nRAmHh+HjtZwsZYGPnRy+xIoQ3H3TUvCRMqzCnLB0nWc1ha6jcfVWoXETRI/BxCbZ\nIEKqwGsCpjI4yA8lK9J67oUGSTH506mc1Ay5Y9/LQneDjGV6hDgkySntAQKBgQCc\n07l8VdFD1mplkAgu5T8qdmmyopkNHcSWiYSFkfov2BL3rFYJckPJ0HB/agvMfszX\nKmXLGyWFp8d2mxfiQ9W5Zei944F6hOJudI2jzeqPbeccORcb1B2xwe9UNfhJKyM6\n1jVmIbA+2Spc3pd9V4Lje4EzQ5f1Lcs/FShT04/qWQKBgC3asF0LDRHKpHayVj5X\nvByOsZBFyfWW/OblTRZVOxu6LzXvgFot3Uy8RxNZnDANTE4rkQ1ieQnU/vaYMWo8\noCecCr1uKb7AKI/F0RS6BAWEJXUwGYAsjzUgp8RQpG6UaTQeykjX0MnSFbQhFFF+\nGZMiC+0T4Wj3Dsv70OCCdXi5\n-----END PRIVATE KEY-----\n",
  "ecSha224": "-----BEGIN CERTIFICATE-----\nMIIBezCCASGgAwIBAgIUeTT16MtPpSHZs8lIoi3Qn054LQcwCgYIKoZIzj0EAwEw\nEzERMA8GA1UEAwwIZWNzaGEyMjQwHhcNMjYxMTE2MDc0NzQ3WhcNMzYxMTEzMDc0\nNzQ3WjATMREwDwYDVQQDDAhlY3NoYTIyNDBZMBMGByqGSM49AgEGCCqGSM49AwEH\nA0IABIt5cBjiQxtu3SyfmadQzf4hwAIJ97bFww5sS3refgmn2hxCr2HQfk85jJ31\nxd8+6yLYEUzsmuvW0LHEr0Z291CjUzBRMB0GA1UdDgQWBBSPb6jBUNFGApgYSfCD\n2jSLdGIskjAfBgNVHSMEGDAWgBSPb6jBUNFGApgYSfCD2jSLdGIskjAPBgNVHRMB\nAf8EBTADAQH/MAoGCCqGSM49BAMBA0gAMEUCIBaTiS9ID25ypxDMRr1v9agfU+Fv\neGTfeRmNuIvQ5f0DAiEAmnUJ+s3DED8ilNngfqgXa/JVIwHKl9vvYAyWCTBMl9A=\n-----END CERTIFICATE-----\n",
  "ecSha224Key": "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgYfsRKwoVXGXgumBC\n9CFc32NlqUPiY2FJO/js4GogSSahRANCAASLeXAY4kMbbt0sn5mnUM3+IcACCfe2\nxcMObEt63n4Jp9ocQq9h0H5POYyd9cXfPusi2BFM7Jrr1tCxxK9GdvdQ\n-----END PRIVATE KEY-----\n",
  "rsaSha3": "-----BEGIN CERTIFICATE-----\nMIIC/zCCAeegAwIBAgIUNloWDJymSBsJzD5LntvXCsCVfCIwDQYJYIZIAWUDBAMO\nBQAwDzENMAsGA1UEAwwEc2hhMzAeFw0yNjExMTYwNzQ3NDdaFw0zNjExMTMwNzQ3\nNDdaMA8xDTALBgNVBAMMBHNoYTMwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEK\nAoIBAQC2uvuxIFDgeYFzFUEoiqLF1sKlMs/IfCZDru2oWgPV4QtGeLuPdcBhX2GU\nj/AhujH8xWQRVjgfSidP2seWVuOPSx7ORc2J/jQ8sGMTs6PoQ56zH2YHPBCRJMyy\n8FkZzSM2ABocUhD18ZwRbNg6sYuxrtX8I4Qlfwo3+tmYBjEZAiRwSgabVJ3cybGI\nmkQFRjkOR/be860tANh3aHaF5sbAqqZE5SxmlMlFDc1NU7EkOiA2tLMQ53ckDUzg\nW9M2DVGmDA7Zibx/3ZAR6NJx2qOFQY7AP+f5D+9q1bXfuM0/xaB8uIPZ/pyyiHoi\nRNRvF91TJB4HjoVhvlv/Pv/gemP9AgMBAAGjUzBRMB0GA1UdDgQWBBSbMmI6zVi0\nbXGISY1xlL4sY3uiyDAfBgNVHSMEGDAWgBSbMmI6zVi0bXGISY1xlL4sY3uiyDAP\nBgNVHRMBAf8EBTADAQH/MA0GCWCGSAFlAwQDDgUAA4IBAQAvHNyn6iZzZJLRcR2u\njTSpsM+R0qNwzZU1hGeLLO7m0eZcGVW/2y5ubPqIRSjbuMG9KmafhWIkExeKM2z3\nP447/J5sOPO8s0Wiv4ZdUVZBS7AZUJkjy9MUeZdFKfweI3xQs3lRAKPnKHe967TO\nOz8/Au8Os2SC8Wzniob104RNEKBsfFvZgZ3jLHdy2DNPyUBSFWU1k8bi7LTJga4e\nDxvKvZB6+kQrfpN9FIT7Tijj6gqCDkuU9lzvpFyZpfsw1C63g28FK8h2EYXjKTZq\n6FowEnHpN93QbFk0GwYlv3LIx13ViJtgQdZvsN+zbItKkW/kZQQA/yThl+C4RTWi\nPWJl\n-----END CERTIFICATE-----\n",
  "rsaSha3Key": "-----BEGIN PRIVATE KEY-----\nMIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQC2uvuxIFDgeYFz\nFUEoiqLF1sKlMs/IfCZDru2oWgPV4QtGeLuPdcBhX2GUj/AhujH8xWQRVjgfSidP\n2seWVuOPSx7ORc2J/jQ8sGMTs6PoQ56zH2YHPBCRJMyy8FkZzSM2ABocUhD18ZwR\nbNg6sYuxrtX8I4Qlfwo3+tmYBjEZAiRwSgabVJ3cybGImkQFRjkOR/be860tANh3\naHaF5sbAqqZE5SxmlMlFDc1NU7EkOiA2tLMQ53ckDUzgW9M2DVGmDA7Zibx/3ZAR\n6NJx2qOFQY7AP+f5D+9q1bXfuM0/xaB8uIPZ/pyyiHoiRNRvF91TJB4HjoVhvlv/\nPv/gemP9AgMBAAECggEAH4Aa6QU+4/+WRQJOPgCuQEg+PE/G2ZMrXoIepqXD4QA/\nWxnvjXLZY//XGM76cLoCKYbWshVord4w6jd3Li/S4U4sJJeMcAQyOZ8XqVsPv7e+\neTcJtegRHBBZuGQuHABM2F+KoMS190v8IVXf7bhTMmGHMoTl4DfnCVD1Z1tz5dE5\nw0OpwtRL3SeyI8Isdx1S5i9iR7nfFFw/l4XdzdmAfTCaNdf4tKZ75S7niaNyZEmD\nVL3kowWPHW8oVd8KrIVsI0RBvOlneyFhBouGTCLtuVjdyOtZSz7+0GA5P0/zGGh1\nFmI2IcTZ9QM4ZBbXZgos6OwzQS/k6nGWiYmE6upZ2QKBgQDmL5M+UX0XFt7sdjBy\nBL6f1Zb2ZSFftCaSwVeuZJojQlwNH5zO6zSWVpvSe7HNhHuCV8NaDc42HS1J//Yi\nSNISGTM5oETQ5dbpofhsjDoCblsRL4FcXRPZPmkQfTEEQnD9wcTHT/LpHBRyZHEG\ngTEUaPC3XVNtAtCZoO0RPVpYSQKBgQDLOQF9YeNLKzEaf7K3VMPZeM+gdb1HdNiH\nU5yLBi+nwTIIXWCkwRa2q1UrRPJBcJqaQyMwlBAxXRxHcXQ7VCjUBvMPn9s47/WM\n1AY+yIK9MTHLwxoM6EFa37/3qnsWLSKR4vqRY/vnHkONfLXayoun1emYbXGVQBVl\nh7Y3y2P2FQKBgQCbi76WN/6toZjWdS9bfPVLkMvIiKI6x8PhG6GiiPzXoroNbDVM\nZghNJw14+Hk4BbRoaeHqcrXMP38hKEQs+sRp0nmZEUIvX4EQs5Dp2QBCMEx9mnpx\n7bvB67iXmeGLnVI5R70vakkdWQpAGrt9x8NgpWE0p23+lg5cIjDQm4L3cQKBgFC8\nEO9y2A2g1x0j5igXMMdQy4t/uvaMYQlCGkrQ5lXK1smj7yUE4LUpU+igt21wAruO\nOIiwCc7Sos1vMTHwuVeieyWV6E2yZubPxLtkhSwMQzAkDlFaY7xqb6VAwS2KONWk\nSs8/3W9wAn5PMR/+n4TJ4sd/k0Qr9VQkmiuITeoRAoGBANaxpE6jrDvl0LpsXrvm\nBznxUgnfivS6sSSGqVD9L9Y0sIIxna8pY95L5Cwtx/85dPE6LkqtXn4cEFcMQxEI\nrdD8xjbF1qlnmqMeWY52sPq73RaOnEqZqkSNWeZgjqYDBsG/sEu8DClryPg4EqkV\nSHrcqJJoiABXk5cTxY3hNFKM\n-----END PRIVATE KEY-----\n",
  "numericCN": "-----BEGIN CERTIFICATE-----\nMIIBCzCBsaADAgECAgEJMAoGCCqGSM49BAMCMA4xDDAKBgNVBAMSAzEyMzAgFw0y\nNDAxMDEwMDAwMDBaGA8yMDYwMDEwMTAwMDAwMFowDjEMMAoGA1UEAxIDMTIzMFkw\nEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEq6Cr01mu8ze9IBqA8hXhOqukBNWJCSP/\n+VVnxwcU77DxwEyVmJrxFajgDTsfJwYUfNZT5flhOtB+5ySGKw7AHzAKBggqhkjO\nPQQDAgNJADBGAiEAu3fBdbgdfcCvgLFlsVHay6DQnte4CAwEQXWaLZ2gTagCIQCF\nXDg8+QY0b15K1edonpyGUXL+ROCmPy7BGNjB70nyDw==\n-----END CERTIFICATE-----\n"
};
results.otherNames = outcome(() => {
  const o = new X509Certificate(more.otherName);
  return [o.subjectAltName, o.toLegacyObject().subjectaltname];
});
results.rdnBoundaries = outcome(() => {
  const leafOf = new X509Certificate(more.twoRDNIssuer);
  const oneCN = new X509Certificate(more.oneCN);
  return [leafOf.checkIssued(oneCN), leafOf.issuer, oneCN.subject];
});
results.fromDer = outcome(() => new X509Certificate(leaf.raw).fingerprint256 === leaf.fingerprint256);
results.fromUint8Array = outcome(() => new X509Certificate(new Uint8Array(Buffer.from(certs.leaf))).serialNumber);
results.withLeadingText = outcome(() => new X509Certificate('junk\n' + certs.leaf).serialNumber);
results.bad = [outcome(() => new X509Certificate('nope')), outcome(() => new X509Certificate(Buffer.from('xx'))),
  outcome(() => new X509Certificate(5)), outcome(() => new X509Certificate()),
  // OpenSSL's reason for bad DER depends on where it fails; only the class is compared.
  (() => { try { new X509Certificate(certs.leaf.replace('CERTIFICATE-----\n', 'CERTIFICATE-----\nAAAA')); return 'parsed'; } catch (e) { return e.name; } })()];

results.checkIssued = [leaf.checkIssued(ca), ca.checkIssued(leaf), ca.checkIssued(ca), odd.checkIssued(odd), leaf.checkIssued(odd), ed.checkIssued(ed)];
const loose = new X509Certificate(certs.looseIssuer);
const sameName = new X509Certificate(certs.edSameName);
const emptySubject = parsed.emptySubject;
results.checkIssuedMore = [loose.checkIssued(ca), loose.verify(ca.publicKey), leaf.checkIssued(sameName),
  emptySubject.checkIssued(ca), ca.checkIssued(emptySubject)];
// A name value that starts with '#' is still a string, compared without regard to case.
const hashCA = new X509Certificate(more.hashCA);
const hashLeaf = new X509Certificate(more.hashLeaf);
results.checkIssuedHashName = [hashLeaf.issuer, hashCA.subject, hashLeaf.checkIssued(hashCA), hashLeaf.verify(hashCA.publicKey)];
// Signature algorithms crypto/x509 doesn't name still verify.
results.unnamedSigAlgs = ['rsaSha224', 'ecSha224', 'rsaSha3'].map((name) => {
  const cert = new X509Certificate(more[name]);
  return [cert.verify(cert.publicKey), cert.checkIssued(cert), cert.checkPrivateKey(crypto.createPrivateKey(more[name + 'Key'])),
    cert.verify(leaf.publicKey)];
});
// A NumericString name value is text, as other string types are.
results.numericString = outcome(() => {
  const cert = new X509Certificate(more.numericCN);
  return [cert.subject, cert.issuer, cert.checkHost('123'), cert.checkIssued(cert), cert.verify(cert.publicKey)];
});
// Getters that return objects give the same object each time.
results.cachedGetters = [leaf.publicKey === leaf.publicKey, leaf.raw === leaf.raw, ca.keyUsage === ca.keyUsage, Array.isArray(ca.keyUsage)];
results.checkIssuedBad = outcome(() => leaf.checkIssued('x'));
results.verify = [leaf.verify(ca.publicKey), leaf.verify(leaf.publicKey), ca.verify(ca.publicKey), ed.verify(ed.publicKey), odd.verify(odd.publicKey)];
// The message for a key of the wrong type quotes util.inspect of the key, which differs.
const codeOnly = (o) => (o.code ? { name: o.name, code: o.code } : o);
results.verifyBad = [outcome(() => leaf.verify('x')), codeOnly(outcome(() => leaf.verify(crypto.createPrivateKey(certs.caKey))))];
results.checkPrivateKey = [leaf.checkPrivateKey(crypto.createPrivateKey(certs.leafKey)), leaf.checkPrivateKey(crypto.createPrivateKey(certs.caKey)),
  ca.checkPrivateKey(crypto.createPrivateKey(certs.caKey))];
results.checkPrivateKeyBad = [outcome(() => leaf.checkPrivateKey('x')), codeOnly(outcome(() => leaf.checkPrivateKey(ca.publicKey)))];

const hosts = ['www.example.com', 'WWW.Example.COM', 'a.example.org', 'b.a.example.org', 'example.org', 'example.com',
  'foo1.example.net', 'foo.example.net', 'x.example.net', 'host.example.com', 'Example Root CA', '127.0.0.1', '', 'www.example.com.'];
results.checkHost = {};
for (const h of hosts) {
  results.checkHost[h] = [leaf, odd, ca, ed].map((c) => show(c.checkHost(h)));
}
results.checkHostOptions = {
  noWildcards: show(leaf.checkHost('a.example.org', { wildcards: false })),
  noPartial: show(leaf.checkHost('foo1.example.net', { partialWildcards: false })),
  multiLabel: show(leaf.checkHost('b.a.example.org', { multiLabelWildcards: true })),
  subjectAlways: show(leaf.checkHost('www.example.com', { subject: 'always' })),
  subjectNever: show(odd.checkHost('host.example.com', { subject: 'never' })),
  subjectAlwaysOdd: show(odd.checkHost('host.example.com', { subject: 'always' })),
  singleLabel: show(leaf.checkHost('.example.org', { singleLabelSubdomains: true })),
};
results.checkHostBad = [outcome(() => leaf.checkHost(5)), outcome(() => leaf.checkHost('x', { subject: 'bogus' })),
  outcome(() => leaf.checkHost('x', { wildcards: 'yes' })), outcome(() => leaf.checkHost('x', 5))];
results.checkEmail = ['admin@example.com', 'ADMIN@EXAMPLE.COM', 'Admin@example.com', 'other@example.com', 'ca@example.com']
  .map((e) => [leaf, ca].map((c) => show(c.checkEmail(e))));
results.checkEmailSubject = [show(ca.checkEmail('ca@example.com', { subject: 'always' })), show(ca.checkEmail('ca@example.com', { subject: 'never' }))];
results.checkEmailCase = [show(leaf.checkEmail('admin@EXAMPLE.COM')), show(ca.checkEmail('ca@EXAMPLE.com')), show(ca.checkEmail('CA@example.com'))];
results.nulNames = [outcome(() => leaf.checkHost('www.example.com\0.other.test')), outcome(() => leaf.checkEmail('admin@example.com\0x')),
  outcome(() => leaf.checkHost('\0')), outcome(() => JSON.stringify(leaf.checkIP('127.0.0.1\0')))];
results.checkEmailBad = outcome(() => leaf.checkEmail(5));
results.checkIP = ['127.0.0.1', '::1', '0:0:0:0:0:0:0:1', '2001:db8::5', '2001:DB8:0::5', '1.2.3.4', '::ffff:127.0.0.1']
  .map((ip) => show(leaf.checkIP(ip)));
results.checkIPBad = [outcome(() => leaf.checkIP('not an ip')), outcome(() => leaf.checkIP(5)), outcome(() => leaf.checkIP('127.0.0.1', {}))];

// Orbital lists the part of Node's lists it implements; each name it lists must work.
results.lists = {
  hashesInclude: ['md5', 'sha1', 'sha224', 'sha256', 'sha384', 'sha512', 'sha512-224', 'sha512-256', 'sha3-256',
    'RSA-SHA256', 'sha256WithRSAEncryption', 'id-rsassa-pkcs1-v1_5-with-sha3-512', 'ripemd160', 'blake2b512', 'blake2s256',
    'md5-sha1', 'shake128', 'shake256'].filter((h) => !crypto.getHashes().includes(h)),
  // Node lists shake128/256, which createHmac refuses.
  hashesWork: crypto.getHashes().filter((h) => !h.startsWith('shake')).filter((h) => {
    try { crypto.createHash(h).update('x').digest(); crypto.createHmac(h, 'k').update('x').digest(); return false; } catch { return true; }
  }),
  hashesSorted: crypto.getHashes().join() === crypto.getHashes().slice().sort().join(),
  curvesInclude: ['prime256v1', 'secp384r1', 'secp521r1'].filter((c) => !crypto.getCurves().includes(c)),
  curvesWork: crypto.getCurves().filter((c) => {
    try { crypto.generateKeyPairSync('ec', { namedCurve: c }); return false; } catch { return true; }
  }),
  ciphers: Array.isArray(crypto.getCiphers()),
};

// createHash and createHmac take Node's digest names.
const digests = {};
for (const name of ['sha256', 'SHA256', 'RSA-SHA256', 'sha-256', 'sha224', 'sha512-224', 'sha512-256', 'sha3-224', 'sha3-512', 'md5WithRSAEncryption', 'ssl3-sha1',
  'ripemd160', 'RMD160', 'ripemd160WithRSA', 'blake2b512', 'BLAKE2S-256', 'md5-sha1']) {
  digests[name] = [crypto.createHash(name).update('abc').digest('hex'), crypto.createHmac(name, 'key').update('abc').digest('hex')];
}
results.digests = digests;
// SHAKE: createHash reads options.outputLength bytes; HMAC refuses it.
results.shake = {
  default128: outcome(() => crypto.createHash('shake128').update('abc').digest('hex')),
  default256: outcome(() => crypto.createHash('SHAKE256').update('abc').digest('hex')),
  length5: outcome(() => crypto.createHash('shake128', { outputLength: 5 }).update('abc').digest('hex')),
  length0: outcome(() => crypto.createHash('shake256', { outputLength: 0 }).update('abc').digest('hex')),
  length100: outcome(() => crypto.createHash('shake256', { outputLength: 100 }).update('abc').digest('base64')),
  copy: outcome(() => {
    const h = crypto.createHash('shake128', { outputLength: 4 }).update('ab');
    return [h.copy().digest('hex'), h.copy({ outputLength: 6 }).digest('hex'), h.digest('hex')];
  }),
  sha256Length32: outcome(() => crypto.createHash('sha256', { outputLength: 32 }).update('abc').digest('hex')),
  sha256Length5: outcome(() => crypto.createHash('sha256', { outputLength: 5 })),
  stringLength: outcome(() => crypto.createHash('shake128', { outputLength: '5' })),
  negativeLength: outcome(() => crypto.createHash('shake128', { outputLength: -1 })),
  fractionLength: outcome(() => crypto.createHash('shake128', { outputLength: 1.5 })),
  hugeLength: outcome(() => crypto.createHash('shake128', { outputLength: 2 ** 32 })),
  unknownWithBadLength: outcome(() => crypto.createHash('nope', { outputLength: 'x' })),
  nullOptions: outcome(() => crypto.createHash('sha256', null).digest('hex')),
  hmac: outcome(() => crypto.createHmac('shake128', 'k')),
};
results.digestErrors = [outcome(() => crypto.createHash('nope')), outcome(() => crypto.createHash(5)),
  outcome(() => crypto.createHmac('nope', 'k')), outcome(() => crypto.createHmac(5, 'k'))];
results.prototype = Object.getOwnPropertyNames(X509Certificate.prototype).sort();

if (globalThis.done) done(results);
else process.stdout.write(JSON.stringify(results));
