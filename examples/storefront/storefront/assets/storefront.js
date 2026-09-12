// storefront.js is the shop's whole client.
//
// It is written without a framework and without a build step for the reason the
// panel's two scripts are: the dependency would cost more than the code it saves,
// and an example that needs a toolchain stops being an example of gobit.
//
// It never sees a secret. The publishable key it sends is visible in the page by
// design — its only authority is naming the sales channel — and the cart id it
// keeps is a capability: knowing it IS the right to the cart, which is why it is
// stored and never shown.
(function () {
  "use strict";

  var shop = document.getElementById("shop");
  if (!shop) {
    return;
  }

  var key = shop.getAttribute("data-key");
  var channel = shop.getAttribute("data-channel");
  var listPath = shop.getAttribute("data-list-path");
  var cartPath = shop.getAttribute("data-cart-path");

  // cartKey is where the cart id is kept between page loads.
  //
  // localStorage rather than a cookie: the id is a capability the SERVER never
  // asks for by itself, so sending it on every request would widen where it can
  // leak. The cost is written down in the example's README — a shared browser
  // keeps the cart.
  var cartKey = "gobit-storefront-cart";

  // call is the one place a request is made, so the header, the error shape and
  // the empty-answer case are decided once.
  function call(path, options) {
    var opts = options || {};
    opts.headers = opts.headers || {};
    opts.headers["x-publishable-api-key"] = key;
    if (opts.body) {
      opts.headers["content-type"] = "application/json";
    }

    return fetch(path, opts).then(function (response) {
      return response.text().then(function (text) {
        var parsed = text ? JSON.parse(text) : {};
        if (!response.ok) {
          var message = parsed.error && parsed.error.message
            ? parsed.error.message
            : "the shop answered " + response.status;
          throw new Error(message);
        }

        return parsed;
      });
    });
  }

  // show replaces the page's contents. Every value reaches the DOM through a
  // text node; there is no innerHTML path in this file, because a product title
  // is somebody else's text.
  function show(nodes) {
    shop.textContent = "";
    nodes.forEach(function (node) {
      shop.appendChild(node);
    });
  }

  function element(tag, text) {
    var node = document.createElement(tag);
    if (text !== undefined && text !== null) {
      node.appendChild(document.createTextNode(String(text)));
    }

    return node;
  }

  function failure(err) {
    var note = element("p", err.message);
    note.className = "notice";
    show([note]);
  }

  // money formats a minor-unit amount with its currency.
  //
  // The scale is NOT assumed to be a hundred: gobit carries the currency's digit
  // count for exactly this reason, and a page that divides by 100 shows yen a
  // hundred times too small. What this example does instead is print the amount
  // and the code without dividing at all — honest, and one decision less for an
  // example to get wrong.
  function money(price) {
    return price.amount + " " + price.currency_code + " (minor units)";
  }

  function firstPrice(variant) {
    var prices = (variant.price_set && variant.price_set.prices) || [];

    return prices.length ? prices[0] : null;
  }

  function catalogPath() {
    return "/store/v1/sales-channels/" + encodeURIComponent(channel) + "/products";
  }

  // --- the catalog -------------------------------------------------------

  function drawList() {
    call(catalogPath() + "?limit=20").then(function (answer) {
      var products = answer.data || [];
      if (!products.length) {
        var empty = element("p",
          "This channel's catalog is empty. A product is only visible here when it is " +
          "published and the channel carries it — see docs/first-run.md.");
        empty.className = "notice";
        show([empty]);

        return;
      }

      var list = element("ul");
      products.forEach(function (product) {
        var item = element("li");
        var link = element("a", product.title);
        link.setAttribute("href", listPath + "/products/" + encodeURIComponent(product.handle));
        item.appendChild(link);

        var variant = (product.variants || [])[0];
        var price = variant ? firstPrice(variant) : null;
        if (price) {
          var tag = element("span", " — " + money(price));
          tag.className = "muted";
          item.appendChild(tag);
        }
        list.appendChild(item);
      });

      show([list]);
    }).catch(failure);
  }

  // --- one product -------------------------------------------------------

  function drawProduct() {
    var handle = shop.getAttribute("data-handle");

    call(catalogPath() + "/" + encodeURIComponent(handle)).then(function (answer) {
      var product = answer.data;
      var nodes = [element("h2", product.title)];

      if (product.description) {
        nodes.push(element("p", product.description));
      }

      var variants = product.variants || [];
      if (!variants.length) {
        nodes.push(element("p", "This product has no variant to sell."));
        show(nodes);

        return;
      }

      variants.forEach(function (variant) {
        var row = element("p");
        var price = firstPrice(variant);
        row.appendChild(document.createTextNode(
          variant.title + (price ? " — " + money(price) : " — no price in this currency")
        ));

        if (price) {
          var button = element("button", "Add to cart");
          button.addEventListener("click", function () {
            addToCart(variant.id, button);
          });
          row.appendChild(document.createTextNode(" "));
          row.appendChild(button);
        }
        nodes.push(row);
      });

      show(nodes);
    }).catch(failure);
  }

  // --- the cart ----------------------------------------------------------

  // openCart returns the stored cart, or opens one.
  //
  // The body carries the COUNTRY and nothing else: the server derives the region
  // and the currency from it, and sending either is refused with 422. The country
  // comes from the shop's own regions, so the example does not hard-code one.
  function openCart() {
    var existing = null;
    try {
      existing = window.localStorage.getItem(cartKey);
    } catch (err) {
      existing = null;
    }
    if (existing) {
      return Promise.resolve(existing);
    }

    return call("/store/v1/regions").then(function (answer) {
      var regions = answer.data || [];
      var country = null;
      regions.some(function (region) {
        var countries = region.countries || [];
        if (countries.length) {
          country = countries[0].iso_2 || countries[0].code;

          return true;
        }

        return false;
      });

      if (!country) {
        throw new Error(
          "no region serves a country yet, so no cart can be opened — " +
          "docs/first-run.md step 3 binds one"
        );
      }

      return call("/store/v1/carts", {
        method: "POST",
        body: JSON.stringify({ country_code: country }),
      }).then(function (created) {
        var id = created.data.id;
        try {
          window.localStorage.setItem(cartKey, id);
        } catch (err) {
          // A browser that refuses storage gets a cart per page load. The shop
          // still works; it just forgets.
        }

        return id;
      });
    });
  }

  function addToCart(variantID, button) {
    button.disabled = true;
    openCart().then(function (cartID) {
      return call("/store/v1/carts/" + encodeURIComponent(cartID) + "/line-items", {
        method: "POST",
        body: JSON.stringify({ variant_id: variantID, quantity: 1 }),
      });
    }).then(function () {
      window.location.href = cartPath;
    }).catch(function (err) {
      button.disabled = false;
      failure(err);
    });
  }

  function drawCart() {
    var id = null;
    try {
      id = window.localStorage.getItem(cartKey);
    } catch (err) {
      id = null;
    }
    if (!id) {
      show([element("p", "The cart is empty.")]);

      return;
    }

    call("/store/v1/carts/" + encodeURIComponent(id)).then(function (answer) {
      var cart = answer.data;
      var items = cart.items || [];
      var nodes = [element("h2", "Cart")];

      if (!items.length) {
        nodes.push(element("p", "The cart is empty."));
        show(nodes);

        return;
      }

      var list = element("ul");
      items.forEach(function (item) {
        list.appendChild(element("li",
          item.title + " x" + item.quantity + " — " + item.total + " " + cart.currency_code));
      });
      nodes.push(list);
      nodes.push(element("p",
        "Total " + cart.total + " " + cart.currency_code + " (minor units), tax " + cart.tax_total));
      nodes.push(element("p",
        "Payment is not part of this example; docs/first-run.md completes the cart with the " +
        "manual provider."));

      show(nodes);
    }).catch(failure);
  }

  var mounts = { list: drawList, product: drawProduct, cart: drawCart };
  var draw = mounts[shop.getAttribute("data-mount")];
  if (draw) {
    draw();
  }
})();
